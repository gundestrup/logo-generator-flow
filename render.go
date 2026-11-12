package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var (
	placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)
	hexColor    = regexp.MustCompile(`^#?([0-9a-fA-F]{6})$`)
)

func wrote(path string) {
	fmt.Println("  wrote", path)
}

// renderSite renders one config into outDir as logo.<format>.
func renderSite(cfg Config, outDir string) error {
	svg, err := renderTemplate(cfg)
	if err != nil {
		return err
	}
	if tb, rerr := os.ReadFile(cfg.Template); rerr == nil && !placeholder.Match(tb) { // #nosec G304 -- config template path
		fmt.Printf("  note: %s: template has no {{…}} placeholders — renders as-is\n", cfg.Name)
	}
	// #nosec G301 -- generated web assets are intentionally world-readable.
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	stem := filepath.Join(outDir, "logo")
	svgPath := stem + ".svg"

	// #nosec G306 -- generated web assets are intentionally world-readable.
	if err := os.WriteFile(svgPath, svg, 0o644); err != nil {
		return fmt.Errorf("write SVG: %w", err)
	}
	wrote(svgPath)

	wanted := formatsSet(cfg.Output.Formats)
	if !wanted["png"] && !wanted["jpg"] && !wanted["webp"] && !wanted["avif"] {
		return nil
	}
	return renderRasters(cfg, wanted, stem, svgPath)
}

func formatsSet(formats []string) map[string]bool {
	wanted := map[string]bool{}
	for _, f := range formats {
		wanted[normalizeFormat(f)] = true
	}
	return wanted
}

// renderRasters renders the master PNG via resvg and converts it into every
// requested derived format. The intermediate PNG is removed unless "png" is
// among the wanted formats.
func renderRasters(cfg Config, wanted map[string]bool, stem, svgPath string) error {
	tools, err := resolveTools(cfg, wanted)
	if err != nil {
		return err
	}
	pngPath := stem + ".png"
	if err := svgToPNG(cfg, tools.resvg, svgPath, pngPath); err != nil {
		return err
	}
	wrote(pngPath)

	converts := []struct {
		format string
		fn     func() error
	}{
		{"jpg", func() error { return pngToJPEG(pngPath, stem+".jpg", cfg.Render.JPEGQuality, cfg.Render.Background) }},
		{"webp", func() error { return pngToWebP(cfg, tools.cwebp, pngPath, stem+".webp") }},
		{"avif", func() error { return pngToAVIF(cfg, tools.avifenc, pngPath, stem+".avif") }},
	}
	for _, c := range converts {
		if !wanted[c.format] {
			continue
		}
		if err := c.fn(); err != nil {
			return err
		}
		wrote(stem + "." + c.format)
	}
	if !wanted["png"] {
		if err := os.Remove(pngPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func renderTemplate(c Config) ([]byte, error) {
	b, err := os.ReadFile(c.Template)
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}
	// fontFamily overrides the template's declared fonts — presentation
	// attributes (either quote style) and CSS font-family: declarations in
	// <style> blocks. Styled [font:…] runs are emitted later, during
	// substitution, and keep their own.
	if c.Render.FontFamily != "" {
		b = fontFamilyAttr.ReplaceAll(b, []byte(`font-family="`+c.Render.FontFamily+`"`))
		b = fontFamilyDecl.ReplaceAll(b, []byte(`font-family:`+c.Render.FontFamily))
	}
	vals := map[string]string{}
	for k, v := range c.Text {
		esc := styledText(v.Value)
		if v.Color != "" {
			esc = `<tspan fill="` + v.Color + `">` + esc + `</tspan>`
		}
		vals[k] = esc
	}
	for k, slot := range c.Images {
		if strings.TrimSpace(slot.Src) == "" {
			// transparent 0x0 svg — keeps href valid so resvg stays quiet.
			vals[k] = emptyImageURI
			continue
		}
		dataURI, err := imageDataURI(slot)
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", k, err)
		}
		vals[k] = html.EscapeString(dataURI)
	}
	result := placeholder.ReplaceAllStringFunc(string(b), func(tok string) string {
		key := placeholder.FindStringSubmatch(tok)[1]
		if v, ok := vals[key]; ok {
			return v
		}
		return tok
	})
	missingSet := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(result, -1) {
		missingSet[m[1]] = true
	}
	if len(missingSet) > 0 {
		keys := make([]string, 0, len(missingSet))
		for k := range missingSet {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("unresolved placeholders: %s", strings.Join(keys, ", "))
	}
	return []byte(result), nil
}

func imageDataURI(img ImageSlot) (string, error) {
	source := img.Src
	if strings.HasPrefix(source, "data:") {
		if img.Fill != "" || img.Stroke != "" {
			return recolorDataURI(source, img)
		}
		return source, nil
	}
	if u, err := url.Parse(source); err == nil && u.Scheme != "" && u.Scheme != "file" {
		return "", errors.New("only local files or data URIs are allowed")
	}
	// #nosec G304 -- image paths come from the site config (operator input).
	b, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	mime, err := mimeFor(source)
	if err != nil {
		return "", err
	}
	if img.Fill != "" || img.Stroke != "" {
		if mime != "image/svg+xml" {
			return "", errors.New("fill/stroke recolor requires an .svg image")
		}
		b = recolorSVG(b, img.Fill, img.Stroke)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}

func recolorDataURI(uri string, img ImageSlot) (string, error) {
	const prefix = "data:image/svg+xml;base64,"
	if !strings.HasPrefix(uri, prefix) {
		return "", errors.New("recolor requires a base64 image/svg+xml data URI")
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	if err != nil {
		return "", err
	}
	return prefix + base64.StdEncoding.EncodeToString(recolorSVG(b, img.Fill, img.Stroke)), nil
}

// emptyImageURI substitutes an empty image slot: a valid, fully
// transparent SVG so <image href> stays resolvable (no resvg warnings).
const emptyImageURI = "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciLz4="

var (
	svgFillAttr    = regexp.MustCompile(`fill="([^"]*)"`)
	svgStrokeAttr  = regexp.MustCompile(`stroke="([^"]*)"`)
	fontFamilyAttr = regexp.MustCompile(`font-family="[^"]*"|font-family='[^']*'`)
	fontFamilyDecl = regexp.MustCompile(`font-family\s*:\s*((?:&[#a-zA-Z0-9]+;|[^;}])*)`)
)

// recolorSVG rewrites fill/stroke attribute values (keeping "none"); a fill
// is injected on the root <svg> when the document declares none at all.
func recolorSVG(b []byte, fill, stroke string) []byte {
	s := string(b)
	if fill != "" {
		if svgFillAttr.MatchString(s) {
			s = svgFillAttr.ReplaceAllStringFunc(s, func(m string) string {
				if m == `fill="none"` {
					return m
				}
				return `fill="` + fill + `"`
			})
		} else {
			s = strings.Replace(s, "<svg", `<svg fill="`+fill+`"`, 1)
		}
	}
	if stroke != "" {
		s = svgStrokeAttr.ReplaceAllStringFunc(s, func(m string) string {
			if m == `stroke="none"` {
				return m
			}
			return `stroke="` + stroke + `"`
		})
	}
	return []byte(s)
}

func mimeFor(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png", nil
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".svg":
		return "image/svg+xml", nil
	case ".webp":
		return "image/webp", nil
	case ".avif":
		return "image/avif", nil
	default:
		return "", fmt.Errorf("unsupported image type: %s", filepath.Ext(path))
	}
}

type siteTools struct {
	resvg, cwebp, avifenc string
}

// resolveTools preflights every external tool the requested formats need and
// reports all missing ones at once, with an install hint.
func resolveTools(c Config, wanted map[string]bool) (siteTools, error) {
	var t siteTools
	var missing []string
	needs := []struct {
		dst        *string
		name       string
		configured string
		needed     bool
	}{
		{&t.resvg, "resvg", c.Tools.Resvg, true},
		{&t.cwebp, "cwebp", c.Tools.CWebP, wanted["webp"]},
		{&t.avifenc, "avifenc", c.Tools.AvifEnc, wanted["avif"]},
	}
	for _, n := range needs {
		if !n.needed {
			continue
		}
		p, err := toolPath(n.configured, n.name)
		if err != nil {
			missing = append(missing, n.name)
			continue
		}
		*n.dst = p
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return t, fmt.Errorf("missing tool(s): %s — install with `brew install resvg webp libavif` or set paths under tools in the config", strings.Join(missing, ", "))
	}
	return t, nil
}

// svgToPNG renders the filled SVG with the resvg CLI.
func svgToPNG(c Config, resvg, svgPath, pngPath string) error {
	var args []string
	if c.Render.Width > 0 {
		args = append(args, "--width", strconv.Itoa(c.Render.Width))
	}
	if c.Render.Height > 0 {
		args = append(args, "--height", strconv.Itoa(c.Render.Height))
	}
	if c.Render.DPI > 0 {
		args = append(args, "--dpi", strconv.FormatFloat(c.Render.DPI, 'f', -1, 64))
	}
	if c.Render.FontFamily != "" {
		args = append(args, "--font-family", c.Render.FontFamily)
	}
	if c.Render.FontsDir != "" {
		args = append(args, "--use-fonts-dir", c.Render.FontsDir)
	}
	for _, f := range c.Render.FontFiles {
		args = append(args, "--use-font-file", f)
	}
	if c.Render.SkipSystemFonts {
		args = append(args, "--skip-system-fonts")
	}
	args = append(args, svgPath, pngPath)
	if err := run(resvg, args...); err != nil {
		return fmt.Errorf("resvg: %w", err)
	}
	return nil
}

func pngToJPEG(src, dst string, quality int, bg string) error {
	// #nosec G304 -- src is the PNG this run just rendered.
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	im, err := png.Decode(f)
	if err != nil {
		return fmt.Errorf("decode PNG: %w", err)
	}
	c, err := parseColor(bg)
	if err != nil {
		return err
	}
	canvas := image.NewRGBA(im.Bounds())
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	draw.Draw(canvas, canvas.Bounds(), im, im.Bounds().Min, draw.Over)
	// #nosec G304 -- dst is inside our own output directory.
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(out, canvas, &jpeg.Options{Quality: quality}); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func pngToWebP(c Config, cwebp, src, dst string) error {
	args := []string{"-quiet", "-mt"}
	if c.Render.WebPLossless {
		args = append(args, "-lossless", "-z", "9", "-exact")
	} else {
		args = append(args, "-q", strconv.Itoa(c.Render.WebPQuality), "-m", "6")
	}
	args = append(args, src, "-o", dst)
	if err := run(cwebp, args...); err != nil {
		return fmt.Errorf("cwebp: %w", err)
	}
	return nil
}

func pngToAVIF(c Config, avifenc, src, dst string) error {
	args := []string{
		"--qcolor", strconv.Itoa(c.Render.AVIFQuality),
		"--qalpha", "100",
		"--speed", strconv.Itoa(c.Render.AVIFSpeed),
		"--yuv", "444",
		src, dst,
	}
	if err := run(avifenc, args...); err != nil {
		return fmt.Errorf("avifenc: %w", err)
	}
	return nil
}

func parseColor(s string) (color.RGBA, error) {
	m := hexColor.FindStringSubmatch(s)
	if m == nil {
		return color.RGBA{}, fmt.Errorf("invalid color %q; use #RRGGBB", s)
	}
	n, err := strconv.ParseUint(m[1], 16, 32)
	if err != nil {
		return color.RGBA{}, err
	}
	return color.RGBA{
		R: uint8(n >> 16 & 0xff),
		G: uint8(n >> 8 & 0xff),
		B: uint8(n & 0xff),
		A: 255,
	}, nil
}

func toolPath(configured, name string) (string, error) {
	if configured != "" {
		p := configured
		if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(p), ".exe") {
			if _, e := os.Stat(p + ".exe"); e == nil {
				p += ".exe"
			}
		}
		if filepath.IsAbs(p) || strings.ContainsAny(p, `/\`) {
			if _, e := os.Stat(p); e != nil {
				return "", fmt.Errorf("tool not found: %s", p)
			}
			return p, nil
		}
		if q, e := exec.LookPath(p); e == nil {
			return q, nil
		}
	}
	if q, e := exec.LookPath(name); e == nil {
		return q, nil
	}
	return "", fmt.Errorf("%s not found; set it under tools in the config or add it to PATH", name)
}

func run(name string, args ...string) error {
	// #nosec G204 -- tool binary resolved from config/PATH; exec is the feature.
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &out)
	cmd.Stderr = io.MultiWriter(os.Stderr, &out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

// styleTok tokenizes the text-slot mini-markup. Bracket tags are constrained
// to safe character sets so attribute injection is impossible.
var styleTok = regexp.MustCompile(`\\.|(\*\*|\*|__|\[(?:color:)?#[0-9A-Fa-f]{3,8}\]|\[font:[A-Za-z0-9 _+.,-]+\]|\[size:[0-9.]+(?:px|em|%)?\]|\[/\])`)

// xmlSafe drops runes that are invalid in XML 1.0 so config text can't
// corrupt the generated SVG document.
func xmlSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0xFFFE || r == 0xFFFF {
			return -1
		}
		return r
	}, s)
}

// styledText converts mini-markup into styled <tspan> runs:
// **bold**, *italic*, __underline__, [#hex]…[/] or [color:#hex]…[/],
// [font:Name]…[/], [size:NN]…[/]. Unmatched markers pass through literally;
// each text run is XML-escaped. \* \_ \[ \] escape to literals.
func styledText(s string) string {
	type sty struct{ w, i, u, fill, fam, size string }
	var cur sty
	var stack []sty
	open := map[byte]bool{}
	var b strings.Builder
	emit := func(seg string) {
		seg = strings.NewReplacer(`\*`, "*", `\_`, "_", `\[`, "[", `\]`, "]").Replace(seg)
		seg = xmlSafe(seg)
		if seg == "" {
			return
		}
		if cur == (sty{}) {
			b.WriteString(html.EscapeString(seg))
			return
		}
		b.WriteString("<tspan")
		attr := func(name, val string) {
			if val != "" {
				fmt.Fprintf(&b, ` %s="%s"`, name, val)
			}
		}
		attr("font-weight", cur.w)
		attr("font-style", cur.i)
		attr("text-decoration", cur.u)
		attr("fill", cur.fill)
		attr("font-family", cur.fam)
		attr("font-size", cur.size)
		b.WriteString(">")
		b.WriteString(html.EscapeString(seg))
		b.WriteString("</tspan>")
	}
	push := func(mut func()) {
		stack = append(stack, cur)
		mut()
	}
	pop := func() {
		if len(stack) > 0 {
			cur = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		}
	}
	idx := 0
	for _, m := range styleTok.FindAllStringSubmatchIndex(s, -1) {
		emit(s[idx:m[0]])
		if m[2] < 0 { // escaped char, written literally via emit of the pair
			emit(s[m[0]:m[1]])
			idx = m[1]
			continue
		}
		tok := s[m[2]:m[3]]
		switch tok {
		case "**":
			if open['b'] {
				pop()
				delete(open, 'b')
			} else {
				open['b'] = true
				push(func() { cur.w = "bold" })
			}
		case "*":
			if open['i'] {
				pop()
				delete(open, 'i')
			} else {
				open['i'] = true
				push(func() { cur.i = "italic" })
			}
		case "__":
			if open['u'] {
				pop()
				delete(open, 'u')
			} else {
				open['u'] = true
				push(func() { cur.u = "underline" })
			}
		case "[/]":
			for k := range open {
				delete(open, k)
			}
			pop()
		default:
			switch {
			case strings.HasPrefix(tok, "[font:"):
				push(func() { cur.fam = strings.TrimSuffix(strings.TrimPrefix(tok, "[font:"), "]") })
			case strings.HasPrefix(tok, "[size:"):
				push(func() { cur.size = strings.TrimSuffix(strings.TrimPrefix(tok, "[size:"), "]") })
			default: // [#hex] or [color:#hex]
				c := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(tok, "[color:"), "["), "]")
				push(func() { cur.fill = c })
			}
		}
		idx = m[1]
	}
	emit(s[idx:])
	return b.String()
}

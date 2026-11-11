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
	vals := map[string]string{}
	for k, v := range c.Text {
		esc := html.EscapeString(v.Value)
		if v.Color != "" {
			esc = `<tspan fill="` + v.Color + `">` + esc + `</tspan>`
		}
		vals[k] = esc
	}
	for k, slot := range c.Images {
		if strings.TrimSpace(slot.Src) == "" {
			vals[k] = "" // empty slot: render nothing
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

var (
	svgFillAttr   = regexp.MustCompile(`fill="([^"]*)"`)
	svgStrokeAttr = regexp.MustCompile(`stroke="([^"]*)"`)
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

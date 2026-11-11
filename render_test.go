package main

import (
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{0, 0, 255, 128})
		}
	}
	// #nosec G304 -- test fixture in a private t.TempDir().
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// fakeTool writes an executable script that copies fixture (or nothing) to its
// last argument — stand-in for resvg/cwebp/avifenc, which all take the output
// path last.
func fakeTool(t *testing.T, dir, name, fixture string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var script string
	if fixture != "" {
		script = "#!/bin/sh\nfor last; do :; done\ncp \"" + fixture + "\" \"$last\"\n"
	} else {
		script = "#!/bin/sh\nfor last; do :; done\ntouch \"$last\"\n"
	}
	// #nosec G306 -- fake tool script must be executable.
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRenderTemplate(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "t.svg")
	writeFile(t, tpl, `<svg><text>{{text1}}</text><text>{{text2}}</text></svg>`)

	cfg := Config{
		Template: tpl,
		Text:     map[string]TextSlot{"text1": {Value: `A <B> & "C"`}, "text2": {Value: ""}},
	}
	got, err := renderTemplate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `A &lt;B&gt; &amp; &#34;C&#34;`) {
		t.Fatalf("text not XML-escaped: %s", s)
	}
	if strings.Contains(s, "{{") {
		t.Fatalf("unresolved placeholder left: %s", s)
	}
}

func TestRenderTemplateUnresolved(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "t.svg")
	writeFile(t, tpl, `<svg>{{text1}} {{image3}}</svg>`)
	_, err := renderTemplate(Config{Template: tpl, Text: map[string]TextSlot{"text1": {Value: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "unresolved placeholders: image3") {
		t.Fatalf("want unresolved error, got %v", err)
	}
}

func TestRenderTemplateEmptyImageSlot(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "t.svg")
	writeFile(t, tpl, `<svg><image href="{{image1}}"/></svg>`)
	got, err := renderTemplate(Config{Template: tpl, Images: map[string]ImageSlot{"image1": {Src: "  "}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `href="data:image/svg+xml;base64,`) {
		t.Fatalf("empty slot should render empty href: %s", got)
	}
}

func TestRenderTemplateImageDataURI(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "pic.png")
	writeTestPNG(t, imgPath)
	tpl := filepath.Join(dir, "t.svg")
	writeFile(t, tpl, `<svg><image href="{{image1}}"/></svg>`)

	got, err := renderTemplate(Config{Template: tpl, Images: map[string]ImageSlot{"image1": {Src: imgPath}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `href="data:image/png;base64,`) {
		t.Fatalf("image not embedded as data URI: %s", got)
	}
}

func TestImageDataURI(t *testing.T) {
	if got, err := imageDataURI(ImageSlot{Src: "data:image/png;base64,AAAA"}); err != nil || got != "data:image/png;base64,AAAA" {
		t.Fatalf("data URI passthrough: %q %v", got, err)
	}
	for _, src := range []string{"https://x/p.png", "http://x/p.png"} {
		if _, err := imageDataURI(ImageSlot{Src: src}); err == nil {
			t.Fatalf("remote URL %q should be rejected", src)
		}
	}
	if _, err := imageDataURI(ImageSlot{Src: filepath.Join(t.TempDir(), "missing.png")}); err == nil {
		t.Fatal("missing file should error")
	}
	dir := t.TempDir()
	gif := filepath.Join(dir, "x.gif")
	writeFile(t, gif, "GIF89a")
	if _, err := imageDataURI(ImageSlot{Src: gif}); err == nil || !strings.Contains(err.Error(), "unsupported image type") {
		t.Fatalf("want unsupported type error, got %v", err)
	}
}

func TestMimeFor(t *testing.T) {
	cases := map[string]string{
		"a.png": "image/png", "a.JPG": "image/jpeg", "a.jpeg": "image/jpeg",
		"a.svg": "image/svg+xml", "a.webp": "image/webp", "a.avif": "image/avif",
	}
	for path, want := range cases {
		got, err := mimeFor(path)
		if err != nil || got != want {
			t.Errorf("mimeFor(%q) = %q,%v want %q", path, got, err, want)
		}
	}
	if _, err := mimeFor("a.txt"); err == nil {
		t.Error("txt should be unsupported")
	}
}

func TestParseColor(t *testing.T) {
	c, err := parseColor("#0369a1")
	if err != nil {
		t.Fatal(err)
	}
	if c.R != 0x03 || c.G != 0x69 || c.B != 0xa1 || c.A != 0xff {
		t.Fatalf("parseColor = %+v", c)
	}
	if _, err := parseColor("#fff"); err == nil {
		t.Error("3-digit hex should be rejected")
	}
	if _, err := parseColor("blue"); err == nil {
		t.Error("named color should be rejected")
	}
}

func TestToolPath(t *testing.T) {
	if p, err := toolPath("/bin/sh", "anything"); err != nil || p != "/bin/sh" {
		t.Fatalf("absolute configured path: %q %v", p, err)
	}
	if _, err := toolPath("/nonexistent/tool", "sh"); err == nil {
		t.Fatal("nonexistent absolute path should error")
	}
	if p, err := toolPath("sh", "definitely-not-sh"); err != nil || p == "" {
		t.Fatalf("configured bare name should resolve via PATH: %q %v", p, err)
	}
	if p, err := toolPath("", "sh"); err != nil || p == "" {
		t.Fatalf("PATH fallback: %q %v", p, err)
	}
	if _, err := toolPath("", "definitely-missing-tool-xyz"); err == nil {
		t.Fatal("missing tool should error")
	}
}

func TestResolveToolsMissing(t *testing.T) {
	cfg := Config{Tools: ToolConfig{
		Resvg: "/nonexistent/resvg", CWebP: "/nonexistent/cwebp", AvifEnc: "/nonexistent/avifenc",
	}}
	_, err := resolveTools(cfg, map[string]bool{"webp": true, "avif": true})
	if err == nil {
		t.Fatal("want missing tools error")
	}
	for _, name := range []string{"avifenc", "cwebp", "resvg"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error should list %s: %v", name, err)
		}
	}
	// webp not requested → cwebp absence must not fail
	if _, err := resolveTools(cfg, map[string]bool{"png": true}); err == nil ||
		strings.Contains(err.Error(), "cwebp") {
		t.Fatalf("cwebp should not be required for png-only: %v", err)
	}
}

func TestPngToJPEG(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.png")
	dst := filepath.Join(dir, "out.jpg")
	writeTestPNG(t, src)
	if err := pngToJPEG(src, dst, 90, "#ffffff"); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- reading our own rendered output in a test.
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xD8 {
		t.Fatal("output is not a JPEG")
	}
}

func TestRenderSiteSVGOnly(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "t.svg")
	writeFile(t, tpl, `<svg><text>{{text1}}</text></svg>`)
	out := filepath.Join(dir, "out", "site")

	cfg := Config{Template: tpl, Text: map[string]TextSlot{"text1": {Value: "Hello"}}}
	cfg.Output.Formats = []string{"svg"}
	if err := renderSite(cfg, out); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- reading our own rendered output in a test.
	b, err := os.ReadFile(filepath.Join(out, "logo.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), ">Hello<") {
		t.Fatalf("placeholder not filled: %s", b)
	}
	if _, err := os.Stat(filepath.Join(out, "logo.png")); !os.IsNotExist(err) {
		t.Fatal("png should not exist for svg-only formats")
	}
}

func TestRenderSiteFakeTools(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	// #nosec G301 -- test fixture in a private t.TempDir().
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "fixture.png")
	writeTestPNG(t, fixture)

	tpl := filepath.Join(dir, "t.svg")
	writeFile(t, tpl, `<svg><text>{{text1}}</text></svg>`)
	out := filepath.Join(dir, "out", "site")

	cfg := Config{
		Template: tpl,
		Text:     map[string]TextSlot{"text1": {Value: "x"}},
		Tools: ToolConfig{
			Resvg:   fakeTool(t, bin, "resvg", fixture),
			CWebP:   fakeTool(t, bin, "cwebp", ""),
			AvifEnc: fakeTool(t, bin, "avifenc", ""),
		},
	}
	applyDefaults(&cfg)
	cfg.Output.Formats = []string{"svg", "png", "jpg", "webp", "avif"}
	if err := renderSite(cfg, out); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"logo.svg", "logo.png", "logo.jpg", "logo.webp", "logo.avif"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing output %s", f)
		}
	}
}

func TestRenderSiteRemovesIntermediatePNG(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	// #nosec G301 -- test fixture in a private t.TempDir().
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(dir, "t.svg")
	writeFile(t, tpl, `<svg><text>{{text1}}</text></svg>`)
	out := filepath.Join(dir, "out", "site")

	cfg := Config{
		Template: tpl,
		Text:     map[string]TextSlot{"text1": {Value: "x"}},
		Tools: ToolConfig{
			Resvg: fakeTool(t, bin, "resvg", ""),
			CWebP: fakeTool(t, bin, "cwebp", ""),
		},
	}
	applyDefaults(&cfg)
	cfg.Output.Formats = []string{"svg", "webp"}
	if err := renderSite(cfg, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "logo.webp")); err != nil {
		t.Fatal("logo.webp missing")
	}
	if _, err := os.Stat(filepath.Join(out, "logo.png")); !os.IsNotExist(err) {
		t.Fatal("intermediate logo.png should be removed")
	}
}

func TestTextSlotColor(t *testing.T) {
	tpl := filepath.Join(t.TempDir(), "t.svg")
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(tpl, []byte(`<svg><text>{{text1}}</text></svg>`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Template: tpl, Text: map[string]TextSlot{
		"text1": {Value: `A & <B>`, Color: "#ff0000"},
	}}
	got, err := renderTemplate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := `<tspan fill="#ff0000">A &amp; &lt;B&gt;</tspan>`
	if !strings.Contains(string(got), want) {
		t.Fatalf("missing %q in %s", want, got)
	}
}

func TestRecolorSVG(t *testing.T) {
	in := `<svg><rect fill="#000000"/><path fill="none"/><circle stroke="#123456"/></svg>`
	out := string(recolorSVG([]byte(in), "#ff0000", "#00ff00"))
	if !strings.Contains(out, `fill="#ff0000"`) || !strings.Contains(out, `fill="none"`) ||
		!strings.Contains(out, `stroke="#00ff00"`) {
		t.Fatalf("recolorSVG = %s", out)
	}
	out = string(recolorSVG([]byte(`<svg><rect/></svg>`), "#112233", ""))
	if !strings.Contains(out, `<svg fill="#112233"`) {
		t.Fatalf("fill injection failed: %s", out)
	}
}

func TestImageRecolor(t *testing.T) {
	dir := t.TempDir()
	svg := filepath.Join(dir, "m.svg")
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(svg, []byte(`<svg><rect fill="#000"/></svg>`), 0o644); err != nil {
		t.Fatal(err)
	}
	uri, err := imageDataURI(ImageSlot{Src: svg, Fill: "#ff0000"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:image/svg+xml;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `fill="#ff0000"`) {
		t.Fatalf("data URI not recolored: %s", b)
	}
	png := filepath.Join(dir, "p.png")
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(png, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := imageDataURI(ImageSlot{Src: png, Fill: "#fff"}); err == nil {
		t.Fatal("expected recolor error for non-svg")
	}
}

func TestStyledText(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain`, `plain`},
		{`a <b>`, `a &lt;b&gt;`},
		{`hi **bold** end`, `hi <tspan font-weight="bold">bold</tspan> end`},
		{`*it*`, `<tspan font-style="italic">it</tspan>`},
		{`__u__`, `<tspan text-decoration="underline">u</tspan>`},
		{`[#f00]red[/]`, `<tspan fill="#f00">red</tspan>`},
		{`[color:#00ff00]g[/]`, `<tspan fill="#00ff00">g</tspan>`},
		{`[font:Menlo]m[/]`, `<tspan font-family="Menlo">m</tspan>`},
		{`[size:40px]big[/]`, `<tspan font-size="40px">big</tspan>`},
		{`**b *i***`, `<tspan font-weight="bold">b </tspan><tspan font-weight="bold" font-style="italic">i</tspan>`},
		{`\*lit\*`, `*lit*`},
		{`[font:<script>]x[/]`, `[font:&lt;script&gt;]x`},
	}
	for _, c := range cases {
		if got := styledText(c.in); got != c.want {
			t.Errorf("styledText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRecolorDataURI(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="#123456"/></svg>`
	uri := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
	out, err := recolorDataURI(uri, ImageSlot{Src: uri, Fill: "#ff0000"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(out, "data:image/svg+xml;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `fill="#ff0000"`) {
		t.Fatalf("recolor not applied: %s", b)
	}
	if _, err := recolorDataURI("data:image/png;base64,AAAA", ImageSlot{Fill: "#f00"}); err == nil {
		t.Fatal("non-svg data URI must be rejected")
	}
	if _, err := recolorDataURI("data:image/svg+xml;base64,!!!notb64", ImageSlot{Fill: "#f00"}); err == nil {
		t.Fatal("bad base64 must error")
	}
}

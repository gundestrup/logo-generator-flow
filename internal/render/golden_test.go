package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"example.com/logo-generator/internal/config"
)

// update regenerates testdata/*.golden — run:
// go test ./internal/render -run Golden -update
var update = flag.Bool("update", false, "update golden test files")

// TestTemplateGolden locks the full generated SVG byte-for-byte against a
// checked-in golden. The fixture exercises the regression classes that only
// surfaced in real output: multi-word font-family quoting, styled-text
// markup, colored slots, svg recolor, and base64 image embedding.
func TestTemplateGolden(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "mark.svg")
	if err := os.WriteFile(mark, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="4" height="4" fill="#123456" stroke="#abcdef"/></svg>`), 0o644); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
	dot := filepath.Join(dir, "dot.png")
	if err := os.WriteFile(dot, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0o644); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
	tpl := filepath.Join(dir, "tpl.svg")
	tplSrc := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 10 10"><style>.t{font-family:Arial;font-size:2px}</style><rect fill="#fff" width="10" height="10"/><text class="t" x="1" y="3">{{text1}}</text><text class="t" x="1" y="6">{{text2}}</text><text x="1" y="9">{{text3}}</text><image href="{{image1}}" x="0" y="0" width="3" height="3"/><image href="{{image2}}" x="5" y="0" width="3" height="3"/></svg>`
	if err := os.WriteFile(tpl, []byte(tplSrc), 0o644); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
	cfg := config.Config{
		Template: tpl,
		Render: config.RenderConfig{
			FontFamily:      "Source Sans 3",
			SkipSystemFonts: true,
		},
		Text: map[string]config.TextSlot{
			"text1": {Value: "[#ff0000]Red[/] & plain"},
			"text2": {Value: "colored", Color: "#00ff00"},
			"text3": {Value: "[font:Source Sans 3]styled[/]", Color: "#0000ff"},
		},
		Images: map[string]config.ImageSlot{
			"image1": {Src: mark, Fill: "#ff0000", Stroke: "#00ff00"},
			"image2": {Src: dot},
		},
	}
	out, err := Template(cfg)
	if err != nil {
		t.Fatal(err)
	}
	gp := filepath.Join("testdata", "template.golden")
	if *update {
		if err := os.WriteFile(gp, out, 0o644); err != nil { // #nosec G306 -- generated golden
			t.Fatal(err)
		}
		t.Logf("updated %s", gp)
	}
	want, err := os.ReadFile(gp) // #nosec G304 -- fixed testdata path in-repo
	if err != nil {
		t.Fatalf("read golden (regen with -update): %v", err)
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("output drifted from %s — regen with -update if intentional\n--- got ---\n%s", gp, out)
	}
	// Invariants the golden file is too bulky to eyeball — assert the
	// regression strings directly too.
	for _, s := range []string{
		`font-family="'Source Sans 3'"`,
		`font-family:'Source Sans 3'`,
		`<tspan fill="#ff0000">Red</tspan>`,
		`<tspan fill="#00ff00">colored</tspan>`,
		`data:image/svg+xml;base64,`,
		`data:image/png;base64,`,
	} {
		if !bytes.Contains(out, []byte(s)) {
			t.Fatalf("golden missing %q", s)
		}
	}
}

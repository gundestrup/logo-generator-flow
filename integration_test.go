//go:build integration

package main

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestIntegrationRealTools renders a site through the real resvg/cwebp/
// avifenc binaries — catches CLI-flag drift that stub tools can't.
// Run: go test -tags=integration -run TestIntegration ./...
func TestIntegrationRealTools(t *testing.T) {
	for _, tool := range []string{"resvg", "cwebp", "avifenc"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not in PATH", tool)
		}
	}
	dir := t.TempDir()
	for _, d := range []string{"configs", "templates", "img"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	writeFile(t, "templates/it.svg", `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="60" viewBox="0 0 120 60">
  <rect width="120" height="60" fill="#ffffff"/>
  <text x="10" y="35" font-size="20">{{text1}}</text>
  <image href="{{image1}}" x="90" y="10" width="24" height="24"/>
</svg>`)
	writeFile(t, "img/mark.svg", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><circle cx="5" cy="5" r="5" fill="#e45842"/></svg>`)
	writeFile(t, "configs/itest.json", `{
  "name": "itest",
  "template": "templates/it.svg",
  "output": {"formats": ["svg", "png", "webp", "avif", "jpeg"]},
  "text": {"text1": "IT"},
  "images": {"image1": "img/mark.svg"}
}`)
	if err := runAll("configs", "output", ""); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"svg", "png", "webp", "avif", "jpg"} {
		p := filepath.Join("output", "itest", "logo."+ext)
		if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
			t.Errorf("missing/empty output %s (err=%v)", p, err)
		}
	}
	f, err := os.Open(filepath.Join("output", "itest", "logo.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 120 || cfg.Height != 60 {
		t.Fatalf("png %dx%d, want 120x60", cfg.Width, cfg.Height)
	}
}

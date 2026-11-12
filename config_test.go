package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() Config {
	c := Config{Name: "site", Template: "t.svg"}
	applyDefaults(&c)
	return c
}

func TestValidateConfig(t *testing.T) {
	ok := func(mutate func(*Config)) Config {
		c := validConfig()
		mutate(&c)
		return c
	}
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"valid", validConfig(), ""},
		{"empty name", ok(func(c *Config) { c.Name = "" }), "invalid or missing name"},
		{"path in name", ok(func(c *Config) { c.Name = "a/b" }), "invalid or missing name"},
		{"dot-dot name", ok(func(c *Config) { c.Name = ".." }), "invalid or missing name"},
		{"no template", ok(func(c *Config) { c.Template = "" }), "template is required"},
		{"bad text slot", ok(func(c *Config) { c.Text = map[string]TextSlot{"text7": {Value: "x"}} }), "invalid text field"},
		{"named text key", ok(func(c *Config) { c.Text = map[string]TextSlot{"title": {Value: "x"}} }), "invalid text field"},
		{"text slots 1-4 ok", ok(func(c *Config) {
			c.Text = map[string]TextSlot{"text1": {Value: "a"}, "text2": {Value: "b"}, "text3": {Value: "c"}, "text4": {Value: "d"}}
		}), ""},
		{"bad image slot", ok(func(c *Config) { c.Images = map[string]ImageSlot{"image0": {Src: "x.png"}} }), "invalid image field"},
		{"quality too high", ok(func(c *Config) { c.Render.WebPQuality = 101 }), "webpQuality must be 1-100"},
		{"avif speed", ok(func(c *Config) { c.Render.AVIFSpeed = 11 }), "avifSpeed must be 0-10"},
		{"negative width", ok(func(c *Config) { c.Render.Width = -1 }), "width/height"},
		{"bad color", ok(func(c *Config) { c.Render.Background = "red" }), "invalid color"},
		{"unknown format", ok(func(c *Config) { c.Output.Formats = []string{"svg", "gif"} }), "unknown format"},
		{"duplicate format", ok(func(c *Config) { c.Output.Formats = []string{"jpg", "jpeg"} }), "duplicate format"},
		{"jpeg alias ok", ok(func(c *Config) { c.Output.Formats = []string{"JPEG"} }), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfig(tc.cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestNormalizeFormat(t *testing.T) {
	cases := map[string]string{
		"svg": "svg", "PNG": "png", " webp ": "webp", "avif": "avif",
		"jpg": "jpg", "jpeg": "jpg", "JPEG": "jpg",
		"gif": "", "": "", "tiff": "",
	}
	for in, want := range cases {
		if got := normalizeFormat(in); got != want {
			t.Errorf("normalizeFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfigFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.json", "a.json", "not-a-config.txt", ".DS_Store"} {
		// #nosec G306 -- test fixtures in a private t.TempDir().
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// #nosec G301 -- test fixture in a private t.TempDir().
	mustMkdir(t, filepath.Join(dir, "sub.json"))

	files, err := configFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")}
	if len(files) != len(want) || files[0] != want[0] || files[1] != want[1] {
		t.Fatalf("configFiles = %v, want %v", files, want)
	}

	single := filepath.Join(dir, "a.json")
	files, err = configFiles(single)
	if err != nil || len(files) != 1 || files[0] != single {
		t.Fatalf("single file: %v %v", files, err)
	}

	empty := t.TempDir()
	if _, err := configFiles(empty); err == nil || !strings.Contains(err.Error(), "no *.json") {
		t.Fatalf("empty dir: want no *.json error, got %v", err)
	}
	if _, err := configFiles(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing path: want error")
	}
}

func TestLoadConfig(t *testing.T) {
	t.Chdir(t.TempDir()) // no assets/fonts here: FontsDir must stay empty

	dir := t.TempDir()
	good := filepath.Join(dir, "site.json")
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(good, []byte(`{
		"name": "site", "template": "t.svg",
		"text": {"text1": "hi"}, "images": {"image1": ""}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(good)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "site" || cfg.Render.JPEGQuality != 92 || cfg.Render.AVIFSpeed != 6 {
		t.Fatalf("defaults not applied: %+v", cfg.Render)
	}
	if got := cfg.Output.Formats; len(got) != 4 {
		t.Fatalf("default formats = %v", got)
	}
	if cfg.Render.FontsDir != "" {
		t.Fatalf("FontsDir should be empty without assets/fonts, got %q", cfg.Render.FontsDir)
	}

	unknown := filepath.Join(dir, "unknown.json")
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(unknown, []byte(`{"name":"x","template":"t","bogus":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(unknown); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want unknown-field error, got %v", err)
	}

	broken := filepath.Join(dir, "broken.json")
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(broken, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(broken); err == nil {
		t.Fatal("want JSON parse error")
	}
}

func TestApplyDefaultsFontsDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// #nosec G301 -- test fixture in a private t.TempDir().
	mustMkdir(t, "assets/fonts")
	var c Config
	applyDefaults(&c)
	if c.Render.FontsDir != "assets/fonts" {
		t.Fatalf("FontsDir = %q, want assets/fonts", c.Render.FontsDir)
	}
	c2 := Config{Render: RenderConfig{FontsDir: "other"}}
	applyDefaults(&c2)
	if c2.Render.FontsDir != "other" {
		t.Fatalf("explicit fontsDir overridden: %q", c2.Render.FontsDir)
	}
}

func TestIsSVGSource(t *testing.T) {
	for src, want := range map[string]bool{
		"a.svg":                          true,
		"dir/B.SVG":                      true,
		"data:image/svg+xml;base64,AAAA": true,
		"a.png":                          false,
		"data:image/png;base64,AAAA":     false,
		"a.svgz":                         false,
		"":                               false,
	} {
		if got := isSVGSource(src); got != want {
			t.Errorf("isSVGSource(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestImageSlotUnmarshal(t *testing.T) {
	var s ImageSlot
	if err := json.Unmarshal([]byte(`"a.svg"`), &s); err != nil || s.Src != "a.svg" {
		t.Fatalf("string form: %v %+v", err, s)
	}
	s = ImageSlot{}
	if err := json.Unmarshal([]byte(`{"src":"b.svg","fill":"#fff","stroke":"#000"}`), &s); err != nil ||
		s.Src != "b.svg" || s.Fill != "#fff" || s.Stroke != "#000" {
		t.Fatalf("object form: %v %+v", err, s)
	}
	for _, bad := range []string{`123`, `{"src":5}`, `{"nope":1}`, `[1]`} {
		if err := json.Unmarshal([]byte(bad), &s); err == nil {
			t.Errorf("input %s should be rejected", bad)
		}
	}
}

func TestValidateSlots(t *testing.T) {
	cases := []struct {
		name    string
		text    map[string]TextSlot
		images  map[string]ImageSlot
		wantErr bool
	}{
		{"bad text key", map[string]TextSlot{"text9": {}}, nil, true},
		{"bad text color", map[string]TextSlot{"text1": {Color: "zzz"}}, nil, true},
		{"bad image key", nil, map[string]ImageSlot{"image9": {}}, true},
		{"bad image fill", nil, map[string]ImageSlot{"image1": {Src: "a.svg", Fill: "zzz"}}, true},
		{"bad image stroke", nil, map[string]ImageSlot{"image1": {Src: "a.svg", Stroke: "zzz"}}, true},
		{"recolor non-svg", nil, map[string]ImageSlot{"image1": {Src: "a.png", Fill: "#ffffff"}}, true},
		{"svg recolor ok", nil, map[string]ImageSlot{"image1": {Src: "a.svg", Fill: "#ffffff", Stroke: "#000000"}}, false},
		{"all empty", nil, nil, false},
	}
	for _, c := range cases {
		cfg := validConfig()
		cfg.Text, cfg.Images = c.text, c.images
		err := validateSlots(cfg)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
	}
}

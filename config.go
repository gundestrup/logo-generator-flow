package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Config is one site definition from a JSON file in the configs directory.
// Relative paths (template, images) resolve against the working directory.
type Config struct {
	Name     string               `json:"name"`
	Template string               `json:"template"`
	Output   OutputConfig         `json:"output"`
	Render   RenderConfig         `json:"render"`
	Tools    ToolConfig           `json:"tools"`
	Text     map[string]TextSlot  `json:"text"`
	Images   map[string]ImageSlot `json:"images"`
}

type OutputConfig struct {
	Formats []string `json:"formats"`
}

type RenderConfig struct {
	Width           int      `json:"width"`
	Height          int      `json:"height"`
	DPI             float64  `json:"dpi"`
	Background      string   `json:"jpegBackground"`
	JPEGQuality     int      `json:"jpegQuality"`
	WebPQuality     int      `json:"webpQuality"`
	WebPLossless    bool     `json:"webpLossless"`
	AVIFQuality     int      `json:"avifQuality"`
	AVIFSpeed       int      `json:"avifSpeed"`
	FontFamily      string   `json:"fontFamily"`
	FontsDir        string   `json:"fontsDir"`
	FontFiles       []string `json:"fontFiles"`
	SkipSystemFonts bool     `json:"skipSystemFonts"`
}

// TextSlot fills a {{textN}} placeholder — a plain string, or
// {"text": "...", "color": "#rrggbb"} to recolor via a <tspan> wrapper.
type TextSlot struct {
	Value string
	Color string
}

func (s *TextSlot) UnmarshalJSON(b []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte(`"`)) {
		return json.Unmarshal(b, &s.Value)
	}
	var o struct {
		Text  string `json:"text"`
		Color string `json:"color"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return err
	}
	s.Value, s.Color = o.Text, o.Color
	return nil
}

// ImageSlot fills an {{imageN}} placeholder — a path/data-URI string, or
// {"src": "...", "fill": "#hex", "stroke": "#hex"}; recolor requires an .svg.
type ImageSlot struct {
	Src    string
	Fill   string
	Stroke string
}

func (s *ImageSlot) UnmarshalJSON(b []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte(`"`)) {
		return json.Unmarshal(b, &s.Src)
	}
	var o struct {
		Src    string `json:"src"`
		Fill   string `json:"fill"`
		Stroke string `json:"stroke"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return err
	}
	s.Src, s.Fill, s.Stroke = o.Src, o.Fill, o.Stroke
	return nil
}

type ToolConfig struct {
	Resvg   string `json:"resvg"`
	CWebP   string `json:"cwebp"`
	AvifEnc string `json:"avifenc"`
}

var (
	siteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	textSlot = regexp.MustCompile(`^text[1-4]$`)
	imgSlot  = regexp.MustCompile(`^image[1-4]$`)
)

func loadConfig(path string) (Config, error) {
	// #nosec G304 -- config path is operator-supplied CLI input.
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("invalid JSON: %w", err)
	}
	applyDefaults(&cfg)
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyDefaults(c *Config) {
	if len(c.Output.Formats) == 0 {
		c.Output.Formats = []string{"svg", "png", "webp", "avif"}
	}
	if c.Render.Background == "" {
		c.Render.Background = "#ffffff"
	}
	if c.Render.JPEGQuality == 0 {
		c.Render.JPEGQuality = 92
	}
	if c.Render.WebPQuality == 0 {
		c.Render.WebPQuality = 90
	}
	if c.Render.AVIFQuality == 0 {
		c.Render.AVIFQuality = 80
	}
	if c.Render.AVIFSpeed == 0 {
		c.Render.AVIFSpeed = 6
	}
	// Project convention: fonts live in assets/fonts. Auto-use it when present.
	if c.Render.FontsDir == "" {
		if info, err := os.Stat("assets/fonts"); err == nil && info.IsDir() {
			c.Render.FontsDir = "assets/fonts"
		}
	}
}

func validateConfig(c Config) error {
	if err := validateIdentity(c); err != nil {
		return err
	}
	if err := validateSlots(c); err != nil {
		return err
	}
	if err := validateRender(c.Render); err != nil {
		return err
	}
	return validateFormats(c.Output.Formats)
}

func validateIdentity(c Config) error {
	if !siteName.MatchString(c.Name) {
		return fmt.Errorf("invalid or missing name %q: use [A-Za-z0-9_-], max 64 chars", c.Name)
	}
	if c.Template == "" {
		return errors.New("template is required")
	}
	return nil
}

func validateSlots(c Config) error {
	for key, s := range c.Text {
		if !textSlot.MatchString(key) {
			return fmt.Errorf("invalid text field %q: use text1-text4", key)
		}
		if err := validColor(s.Color); err != nil {
			return fmt.Errorf("text %s color: %w", key, err)
		}
	}
	for key, s := range c.Images {
		if !imgSlot.MatchString(key) {
			return fmt.Errorf("invalid image field %q: use image1-image4", key)
		}
		for name, col := range map[string]string{"fill": s.Fill, "stroke": s.Stroke} {
			if err := validColor(col); err != nil {
				return fmt.Errorf("image %s %s: %w", key, name, err)
			}
		}
		if (s.Fill != "" || s.Stroke != "") && !isSVGSource(s.Src) {
			return fmt.Errorf("image %s: recolor requires an .svg source", key)
		}
	}
	return nil
}

// validColor rejects malformed colors; empty means "not set".
func validColor(s string) error {
	if s == "" {
		return nil
	}
	_, err := parseColor(s)
	return err
}

func isSVGSource(src string) bool {
	l := strings.ToLower(strings.TrimSpace(src))
	return strings.HasSuffix(l, ".svg") || strings.HasPrefix(l, "data:image/svg")
}

func validateRender(r RenderConfig) error {
	for name, q := range map[string]int{
		"jpegQuality": r.JPEGQuality,
		"webpQuality": r.WebPQuality,
		"avifQuality": r.AVIFQuality,
	} {
		if q < 1 || q > 100 {
			return fmt.Errorf("%s must be 1-100", name)
		}
	}
	if r.AVIFSpeed < 0 || r.AVIFSpeed > 10 {
		return errors.New("avifSpeed must be 0-10")
	}
	if r.Width < 0 || r.Height < 0 {
		return errors.New("width/height must be >= 0")
	}
	_, err := parseColor(r.Background)
	return err
}

func validateFormats(formats []string) error {
	seen := map[string]bool{}
	for _, f := range formats {
		norm := normalizeFormat(f)
		if norm == "" {
			return fmt.Errorf("unknown format: %s", f)
		}
		if seen[norm] {
			return fmt.Errorf("duplicate format: %s", f)
		}
		seen[norm] = true
	}
	return nil
}

func normalizeFormat(f string) string {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case "svg", "png", "webp", "avif":
		return strings.ToLower(strings.TrimSpace(f))
	case "jpg", "jpeg":
		return "jpg"
	}
	return ""
}

// configFiles returns the *.json files to render: a single file, or every
// *.json in the given directory, sorted by name.
func configFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("configs path: %w", err)
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			files = append(files, filepath.Join(path, e.Name()))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no *.json configs in %s", path)
	}
	return files, nil
}

// Package config defines the per-site JSON schema: strict decoding,
// defaults, validation, and the {{slot}} template-token contract.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

// OutputConfig selects which logo.* formats a site emits.
type OutputConfig struct {
	Formats []string `json:"formats"`
}

// RenderConfig controls rasterization: tools, fonts, background, size.
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

// UnmarshalJSON accepts "text" or {"text","color"} forms.
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

// UnmarshalJSON accepts "path" or {"src","fill","stroke"} forms.
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

// ToolConfig overrides PATH lookup for external converters.
type ToolConfig struct {
	Resvg   string `json:"resvg"`
	CWebP   string `json:"cwebp"`
	AvifEnc string `json:"avifenc"`
}

var (
	siteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	textSlot = regexp.MustCompile(`^text[1-6]$`)
	imgSlot  = regexp.MustCompile(`^image[1-6]$`)
	hexColor = regexp.MustCompile(`^#?([0-9a-fA-F]{6})$`)

	// Placeholder matches {{slot}} tokens in SVG templates.
	Placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)
)

// ValidTextSlot reports whether s is a text1-text6 slot name.
func ValidTextSlot(s string) bool { return textSlot.MatchString(s) }

// ValidImageSlot reports whether s is an image1-image6 slot name.
func ValidImageSlot(s string) bool { return imgSlot.MatchString(s) }

// ValidSiteName reports whether s is a legal site/output name.
func ValidSiteName(s string) bool { return siteName.MatchString(s) }

// ParseColor decodes #RRGGBB (optional #) into an opaque RGBA.
func ParseColor(s string) (color.RGBA, error) {
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

// Load reads and validates one site config JSON file.
func Load(path string) (Config, error) {
	// #nosec G304 -- config path is operator-supplied CLI input.
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return Parse(b)
}

// Parse decodes one site config: strict JSON, defaults applied, validated.
func Parse(b []byte) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("invalid JSON: %w", err)
	}
	cfg.ApplyDefaults()
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ApplyDefaults fills unset fields with their documented defaults.
func (c *Config) ApplyDefaults() {
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
			return fmt.Errorf("invalid text field %q: use text1-text6", key)
		}
		if err := validColor(s.Color); err != nil {
			return fmt.Errorf("text %s color: %w", key, err)
		}
	}
	for key, s := range c.Images {
		if !imgSlot.MatchString(key) {
			return fmt.Errorf("invalid image field %q: use image1-image6", key)
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
	_, err := ParseColor(s)
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
	_, err := ParseColor(r.Background)
	return err
}

func validateFormats(formats []string) error {
	seen := map[string]bool{}
	for _, f := range formats {
		norm := NormalizeFormat(f)
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

// NormalizeFormat lowercases a format name and maps jpg→jpeg.
func NormalizeFormat(f string) string {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case "svg", "png", "webp", "avif":
		return strings.ToLower(strings.TrimSpace(f))
	case "jpg", "jpeg":
		return "jpg"
	}
	return ""
}

// Files returns the *.json files to render: a single file, or every
// *.json in the given directory, sorted by name.
func Files(path string) ([]string, error) {
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

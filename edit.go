package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed edit/index.html
var editIndex []byte

// serveEdit starts the loopback-only browser editor: pick a site, edit its
// JSON config, see the rendered SVG update live, and run the full raster
// pipeline into output/<name>/ on demand.
func serveEdit(configsPath, outDir string) error {
	h := &editHandler{configsPath: configsPath, outDir: outDir}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.index)
	mux.HandleFunc("GET /api/sites", h.sites)
	mux.HandleFunc("GET /api/site", h.site)
	mux.HandleFunc("GET /api/images", h.assetList("assets/images", "images"))
	mux.HandleFunc("GET /api/fonts", h.assetList("assets/fonts", "fonts"))
	mux.HandleFunc("GET /asset/", h.assetFile)
	mux.HandleFunc("GET /out/", h.outputFile)
	mux.HandleFunc("POST /api/template-analyze", h.tplAnalyze)
	mux.HandleFunc("POST /api/template-fill", h.tplFill)
	mux.HandleFunc("POST /api/preview", h.preview)
	mux.HandleFunc("POST /api/render", h.renderAll)
	mux.HandleFunc("POST /api/save", h.save)
	addr := "127.0.0.1:8181"
	fmt.Println("editor: http://" + addr + "/  (Ctrl+C to stop)")
	// nosemgrep: go.lang.security.audit.net.use-tls.use-tls
	return http.ListenAndServe(addr, mux) // #nosec G114 -- loopback dev tool, read-only timeouts acceptable
}

type editHandler struct {
	configsPath string
	outDir      string
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func parseConfigBytes(b []byte) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid JSON: %w", err)
	}
	applyDefaults(&cfg)
	if err := validateConfig(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (h *editHandler) configPath(name string) (string, error) {
	if !siteName.MatchString(name) {
		return "", fmt.Errorf("invalid site name %q", name)
	}
	return filepath.Join(h.configsPath, name+".json"), nil
}

func (h *editHandler) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(editIndex)
}

func (h *editHandler) sites(w http.ResponseWriter, _ *http.Request) {
	files, err := configFiles(h.configsPath)
	if err != nil {
		jsonErr(w, err)
		return
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": names})
}

func (h *editHandler) site(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	path, err := h.configPath(name)
	if err != nil {
		jsonErr(w, err)
		return
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- name is regex-validated, path joins configsPath
	if err != nil {
		jsonErr(w, err)
		return
	}
	var tpl string
	var cfg Config
	if json.Unmarshal(raw, &cfg) == nil && cfg.Template != "" {
		if b, rerr := os.ReadFile(cfg.Template); rerr == nil { // #nosec G304 -- template path comes from the site config
			tpl = string(b)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":     name,
		"config":   string(raw),
		"template": tpl,
	})
}

func (h *editHandler) preview(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		jsonErr(w, err)
		return
	}
	cfg, err := parseConfigBytes(body)
	if err != nil {
		jsonErr(w, err)
		return
	}
	svg, err := renderTemplate(cfg)
	if err != nil {
		jsonErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"svg": string(svg)})
}

func (h *editHandler) renderAll(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		jsonErr(w, err)
		return
	}
	cfg, err := parseConfigBytes(body)
	if err != nil {
		jsonErr(w, err)
		return
	}
	if err := renderSite(cfg, filepath.Join(h.outDir, cfg.Name)); err != nil {
		jsonErr(w, err)
		return
	}
	files := make([]string, 0, len(cfg.Output.Formats))
	for _, f := range cfg.Output.Formats {
		if norm := normalizeFormat(f); norm != "" {
			files = append(files, cfg.Name+"/logo."+norm)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (h *editHandler) save(w http.ResponseWriter, r *http.Request) {
	path, err := h.configPath(r.URL.Query().Get("name"))
	if err != nil {
		jsonErr(w, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		jsonErr(w, err)
		return
	}
	if _, err := parseConfigBytes(body); err != nil {
		jsonErr(w, err)
		return
	}
	if err := os.WriteFile(path, body, 0o644); err != nil { // #nosec G306 -- site configs are committed files, not secrets
		jsonErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"saved": path})
}

// assetList serves the contents of an assets subdirectory as
// {key: [{name, path, svg}]}; a missing dir yields an empty list.
func (h *editHandler) assetList(dir, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		items := []map[string]any{}
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				items = append(items, map[string]any{
					"name": e.Name(),
					"path": filepath.ToSlash(filepath.Join(dir, e.Name())),
					"svg":  strings.HasSuffix(strings.ToLower(e.Name()), ".svg"),
				})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{key: items})
	}
}

// assetFile serves files under assets/images or assets/fonts for previews.
func (h *editHandler) assetFile(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/asset/")
	rel := filepath.FromSlash(strings.TrimPrefix(
		filepath.Clean("/"+strings.Trim(filepath.FromSlash(p), "/")), "/"))
	sub := strings.SplitN(rel, "/", 2)
	if len(sub) != 2 || (sub[0] != "images" && sub[0] != "fonts") {
		jsonErr(w, fmt.Errorf("invalid asset path"))
		return
	}
	http.ServeFile(w, r, filepath.Join("assets", rel)) // #nosec G703 -- path is root-anchored Clean + restricted to images|fonts
}

func (h *editHandler) outputFile(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/out/")
	// Anchor cleaning at "/" so ".." can never escape outDir.
	rel := filepath.FromSlash(filepath.Clean("/" + strings.Trim(filepath.FromSlash(p), "/")))
	http.ServeFile(w, r, filepath.Join(h.outDir, rel)) // #nosec G703 -- path is root-anchored Clean, cannot escape outDir
}

// ---- template analysis & placeholder fill (editor "map template" flow) ----

// tplElement is one id'd SVG element that can act as a replacement region.
type tplElement struct {
	ID   string  `json:"id"`
	Tag  string  `json:"tag"`
	Slot string  `json:"slot,omitempty"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

var (
	tplElemRe = regexp.MustCompile(`<(path|rect|image|circle|ellipse)\b[^>]*\bid="([^"]+)"[^>]*>|<text\b[^>]*\bid="([^"]+)"[^>]*>(.*?)</text>`)
	tplDRe    = regexp.MustCompile(`\bd="([^"]*)"`)
	tplNumRe  = regexp.MustCompile(`-?\d+\.?\d*`)
	tplAttrRe = regexp.MustCompile(`\b(x|y|width|height)="(-?\d+\.?\d*)"`)
	tplXYRe   = regexp.MustCompile(`matrix\(1 0 0 1 (-?\d+\.?\d*) (-?\d+\.?\d*)\)`)
	tplVBRe   = regexp.MustCompile(`viewBox="(-?[\d.]+)[ ,]+(-?[\d.]+)[ ,]+([\d.]+)[ ,]+([\d.]+)"`)
)

// analyzeTemplate finds id'd elements and their bounding boxes. For <path>
// the box is min/max over the d attribute's coordinate pairs — exact for
// absolute-coordinate outlines like the figure boxes, approximate otherwise.
func analyzeTemplate(svg string) []tplElement {
	els := []tplElement{}
	for _, m := range tplElemRe.FindAllStringSubmatch(svg, -1) {
		e := tplElement{Tag: "text", ID: m[3]}
		if m[1] != "" {
			e.Tag, e.ID = m[1], m[2]
		}
		if p := placeholder.FindStringSubmatch(m[0]); p != nil {
			e.Slot = p[1]
		}
		switch e.Tag {
		case "path":
			var nums []string
			if dm := tplDRe.FindStringSubmatch(m[0]); dm != nil {
				nums = tplNumRe.FindAllString(dm[1], -1)
			}
			for i := 0; i+1 < len(nums); i += 2 {
				x, y := atof(nums[i]), atof(nums[i+1])
				if e.W == 0 && e.H == 0 && i == 0 {
					e.X, e.Y = x, y
				}
				e.X, e.Y = min(e.X, x), min(e.Y, y)
				e.W, e.H = max(e.W, x), max(e.H, y)
			}
			e.W, e.H = e.W-e.X, e.H-e.Y
		case "text":
			if t := tplXYRe.FindStringSubmatch(m[0]); t != nil {
				e.X, e.Y = atof(t[1]), atof(t[2])
			}
		default:
			attrs := map[string]float64{}
			for _, a := range tplAttrRe.FindAllStringSubmatch(m[0], -1) {
				attrs[a[1]] = atof(a[2])
			}
			if e.Tag == "circle" {
				e.X, e.Y = attrs["x"]-attrs["width"], attrs["y"]-attrs["width"]
				e.W, e.H = 2*attrs["width"], 2*attrs["width"]
			} else {
				e.X, e.Y, e.W, e.H = attrs["x"], attrs["y"], attrs["width"], attrs["height"]
			}
		}
		els = append(els, e)
	}
	return els
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// templateFill replaces mapped elements with {{slot}} placeholders: figures
// become <image> elements sized to the element's box (image keeps its aspect
// ratio via xMidYMid meet). For <text>, multiple slots mapped to one id are
// distributed across its tspans in slot order (extra tspans are emptied) so
// styled segments keep their classes.
func templateFill(svg string, mapping map[string]string) (string, error) {
	byID := map[string][]string{}
	ids := []string{}
	for slot, id := range mapping {
		if _, seen := byID[id]; !seen {
			ids = append(ids, id)
		}
		byID[id] = append(byID[id], slot)
	}
	sort.Strings(ids)
	for _, id := range ids {
		slots := byID[id]
		sort.Strings(slots)
		re := regexp.MustCompile(`<(path|rect|image|circle|ellipse)\b[^>]*\bid="` + regexp.QuoteMeta(id) + `"[^>]*/>`)
		tre := regexp.MustCompile(`(<text\b[^>]*\bid="` + regexp.QuoteMeta(id) + `"[^>]*>)(.*?)(</text>)`)
		if loc := re.FindStringIndex(svg); loc != nil {
			var box tplElement
			for _, e := range analyzeTemplate(svg[loc[0]:loc[1]]) {
				box = e
			}
			img := fmt.Sprintf(`<image id="%s" href="{{%s}}" x="%v" y="%v" width="%v" height="%v" preserveAspectRatio="xMidYMid meet"/>`,
				id, slots[0], box.X, box.Y, box.W, box.H)
			svg = svg[:loc[0]] + img + svg[loc[1]:]
			continue
		}
		m := tre.FindStringSubmatchIndex(svg)
		if m == nil {
			return "", fmt.Errorf("element id %q not found in template", id)
		}
		inner := svg[m[4]:m[5]]
		tspans := regexp.MustCompile(`<tspan\b[^>]*>.*?</tspan>`).FindAllString(inner, -1)
		filled := []string{}
		for i, t := range tspans {
			if i < len(slots) {
				open := t[:strings.Index(t, ">")+1]
				filled = append(filled, open+"{{"+slots[i]+"}}</tspan>")
			} else {
				open := t[:strings.Index(t, ">")+1]
				filled = append(filled, open+"</tspan>")
			}
		}
		if len(slots) > len(tspans) && len(tspans) > 0 {
			extra := ""
			for _, s := range slots[len(tspans):] {
				extra += "{{" + s + "}}"
			}
			filled[len(filled)-1] = strings.TrimSuffix(filled[len(filled)-1], "</tspan>") + extra + "</tspan>"
		}
		svg = svg[:m[4]] + strings.Join(filled, "") + svg[m[5]:]
	}
	return svg, nil
}

func (h *editHandler) tplAnalyze(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		jsonErr(w, err)
		return
	}
	var cfg Config
	if json.Unmarshal(body, &cfg) != nil || cfg.Template == "" {
		jsonErr(w, fmt.Errorf("config needs a template path"))
		return
	}
	b, err := os.ReadFile(cfg.Template) // #nosec G304 -- template path comes from the posted config
	if err != nil {
		jsonErr(w, err)
		return
	}
	vb := []string{}
	if m := tplVBRe.FindStringSubmatch(string(b)); m != nil {
		vb = m[1:]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"elements": analyzeTemplate(string(b)),
		"viewBox":  vb,
	})
}

type tplFillReq struct {
	Config  json.RawMessage   `json:"config"`
	Mapping map[string]string `json:"mapping"`
	Out     string            `json:"out"`
}

func (h *editHandler) tplFill(w http.ResponseWriter, r *http.Request) {
	var req tplFillReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		jsonErr(w, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	cfg, err := parseConfigBytes(req.Config)
	if err != nil {
		jsonErr(w, err)
		return
	}
	b, err := os.ReadFile(cfg.Template) // #nosec G304 -- template path comes from the posted config
	if err != nil {
		jsonErr(w, err)
		return
	}
	out := templateFillValidate(req.Mapping)
	if err := out; err != nil {
		jsonErr(w, err)
		return
	}
	filled, err := templateFill(string(b), req.Mapping)
	if err != nil {
		jsonErr(w, err)
		return
	}
	dst := strings.TrimSuffix(cfg.Template, filepath.Ext(cfg.Template)) + "-filled.svg"
	if req.Out != "" {
		if !strings.HasPrefix(req.Out, "templates/") || strings.Contains(req.Out, "..") || !strings.HasSuffix(req.Out, ".svg") {
			jsonErr(w, fmt.Errorf("out must stay under templates/ and end in .svg"))
			return
		}
		dst = req.Out
	}
	// #nosec G306 G703 -- dst is constrained to templates/*.svg or derived from the config template path.
	if err := os.WriteFile(dst, []byte(filled), 0o644); err != nil {
		jsonErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"template": dst})
}

func templateFillValidate(mapping map[string]string) error {
	for slot := range mapping {
		if !textSlot.MatchString(slot) && !imgSlot.MatchString(slot) {
			return fmt.Errorf("invalid slot %q", slot)
		}
	}
	return nil
}

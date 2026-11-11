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
	mux.HandleFunc("GET /out/", h.outputFile)
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

func (h *editHandler) outputFile(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/out/")
	// Anchor cleaning at "/" so ".." can never escape outDir.
	rel := filepath.FromSlash(filepath.Clean("/" + strings.Trim(filepath.FromSlash(p), "/")))
	http.ServeFile(w, r, filepath.Join(h.outDir, rel))
}

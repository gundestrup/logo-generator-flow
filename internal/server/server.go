// Package server hosts the loopback browser editor (-ui): config CRUD,
// live SVG preview, template mapping, and on-demand raster renders.
package server

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"example.com/logo-generator/internal/config"
	"example.com/logo-generator/internal/fonts"
	"example.com/logo-generator/internal/render"
)

//go:embed static
var staticFS embed.FS

// Mux builds the editor's route table; kept separate from Serve so
// tests exercise the real registered routes instead of re-dispatching.
// Mux builds the editor's route table; kept separate from Serve so
// tests exercise the real registered routes instead of re-dispatching.
func Mux(h *handler, onShutdown func()) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.index)
	mux.HandleFunc("GET /favicon.ico", h.favicon)
	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/",
		cacheStatic(http.FileServerFS(sub))))
	mux.HandleFunc("GET /api/sites", h.sites)
	mux.HandleFunc("GET /api/site", h.site)
	mux.HandleFunc("GET /api/images", h.assetList("assets/images", "images"))
	mux.HandleFunc("GET /api/fonts", h.assetList("assets/fonts", "fonts"))
	mux.HandleFunc("GET /api/templates", h.assetList("assets/templates", "templates"))
	mux.HandleFunc("GET /fonts.css", h.fontsCSS)
	mux.HandleFunc("GET /asset/", h.assetFile)
	mux.HandleFunc("GET /out/", h.outputFile)
	mux.HandleFunc("POST /api/template-analyze", h.tplAnalyze)
	mux.HandleFunc("POST /api/template-fill", h.tplFill)
	mux.HandleFunc("POST /api/preview", h.preview)
	mux.HandleFunc("POST /api/render", h.renderAll)
	mux.HandleFunc("POST /api/save", h.save)
	mux.HandleFunc("POST /api/duplicate", h.duplicate)
	mux.HandleFunc("POST /api/rename", h.rename)
	mux.HandleFunc("POST /api/delete", h.deleteConfig)
	mux.HandleFunc("GET /api/ping", h.ping)
	mux.HandleFunc("POST /api/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		go onShutdown()
	})
	return mux
}

// Serve starts the loopback-only browser editor: pick a site, edit its
// JSON config, see the rendered SVG update live, and run the full raster
// pipeline into output/<name>/ on demand.
func Serve(configsPath, outDir, addr, version string) error {
	h := &handler{configsPath: configsPath, outDir: outDir, version: version}
	srv := &http.Server{} // #nosec G112 -- loopback dev tool
	srv.Handler = Mux(h, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if !stopStaleEditor(addr) {
			return fmt.Errorf("editor: %w (port held by a different process?)", err)
		}
		if ln, err = listenWhenFree(addr, 3*time.Second); err != nil {
			return err
		}
	}
	fmt.Println("editor: http://" + addr + "/  (Ctrl+C to stop)")
	// nosemgrep: go.lang.security.audit.net.use-tls.use-tls
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) { // #nosec G114 -- loopback dev tool
		return err
	}
	return nil
}

// ping identifies this editor so a newly started instance can recognise (and
// stop) a stale process still holding the port.
func (h *handler) ping(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"app":     "logo-generator",
		"version": h.version,
		"pid":     os.Getpid(),
	})
}

// stopStaleEditor asks a previous logo-generator editor on addr to shut down.
// Returns false if whatever holds the port isn't this program.
func stopStaleEditor(addr string) bool {
	cli := &http.Client{Timeout: 500 * time.Millisecond}
	res, err := cli.Get("http://" + addr + "/api/ping") // NOSONAR -- loopback-only dev tool, addr is always 127.0.0.1
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	var p struct {
		App     string `json:"app"`
		Version string `json:"version"`
		PID     int    `json:"pid"`
	}
	if json.NewDecoder(res.Body).Decode(&p) != nil || p.App != "logo-generator" {
		return false
	}
	resp, err := cli.Post("http://"+addr+"/api/shutdown", "", nil) // NOSONAR -- loopback-only dev tool, addr is always 127.0.0.1
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	fmt.Printf("editor: stopped previous instance (pid %d, v%s)\n", p.PID, p.Version)
	return true
}

func listenWhenFree(addr string, timeout time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(timeout)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("editor: port still busy after stopping previous instance: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type handler struct {
	version     string
	configsPath string
	outDir      string
	fontsDir    string // assets/fonts unless overridden (tests)
	fontMu      sync.Mutex
	fontIdx     map[string]string
	fontIdxAt   time.Time
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func (h *handler) configPath(name string) (string, error) {
	if !config.ValidSiteName(name) {
		return "", fmt.Errorf("invalid site name %q", name)
	}
	return filepath.Join(h.configsPath, name+".json"), nil
}

func (h *handler) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	b, _ := staticFS.ReadFile("static/index.html")
	// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter -- serving fixed embedded bytes, not user input
	_, _ = w.Write(b)
}

// cacheStatic makes embedded UI assets revalidate every load — the same
// URL serves different bytes after a rebuild, and a long max-age once
// stranded a stale app.js in the browser for an hour.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func (h *handler) favicon(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16" rx="4" fill="#0b1220"/><path d="M4 8h8M8 4v8" stroke="#38bdf8" stroke-width="2" stroke-linecap="round"/></svg>`))
}

// siteEntry is one configs/*.json file: file is the API key (basename
// without .json), name is the config's own "name" field shown in the UI.
type siteEntry struct {
	Name string `json:"name"`
	File string `json:"file"`
}

func (h *handler) sites(w http.ResponseWriter, _ *http.Request) {
	files, err := config.Files(h.configsPath)
	if err != nil {
		jsonErr(w, err)
		return
	}
	sites := make([]siteEntry, 0, len(files))
	for _, f := range files {
		e := siteEntry{File: strings.TrimSuffix(filepath.Base(f), ".json")}
		if b, rerr := os.ReadFile(f); rerr == nil { // #nosec G304 -- f comes from config.Files under configsPath
			var probe struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(b, &probe) == nil {
				e.Name = probe.Name
			}
		}
		if e.Name == "" {
			e.Name = e.File
		}
		sites = append(sites, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": sites})
}

func (h *handler) site(w http.ResponseWriter, r *http.Request) {
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
	var cfg config.Config
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

func (h *handler) preview(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		jsonErr(w, err)
		return
	}
	cfg, err := config.Parse(body)
	if err != nil {
		jsonErr(w, err)
		return
	}
	svg, err := render.Template(cfg)
	if err != nil {
		jsonErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"svg": h.injectFontFaces(string(svg), cfg)})
}

var fontFamilyVal = regexp.MustCompile(
	`font-family="([^"]*)"|font-family='([^']*)'|font-family\s*:\s*((?:&[#a-zA-Z0-9]+;|[^;}])*)`)

// fontsCSS serves @font-face rules for every font under the fonts dir so
// the editor chrome (pickers, previews) can render each family.
func (h *handler) fontsCSS(w http.ResponseWriter, _ *http.Request) {
	dir := h.fontsDir
	if dir == "" {
		dir = "assets/fonts"
	}
	var rules strings.Builder
	names := make([]string, 0, 8)
	for fam := range h.fontIndex(dir) {
		names = append(names, fam)
	}
	sort.Strings(names)
	for _, fam := range names {
		fmt.Fprintf(&rules, `@font-face{font-family:'%s';font-display:swap;src:url('/asset/fonts/%s')}`,
			cssSafe(fam), url.PathEscape(filepath.Base(h.fontIndex(dir)[fam])))
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// nosemgrep: go.lang.security.audit.xss.no-io-writestring-to-responsewriter.no-io-writestring-to-responsewriter -- generated fonts.css served as text/css from local font metadata
	_, _ = io.WriteString(w, rules.String())
}

// injectFontFaces adds <style>@font-face</style> rules covering every
// font-family the SVG references — configured files plus anything matching
// an assets/fonts file — so the browser preview renders custom fonts.
// Rasters don't need it: resvg loads the same files via --use-font-file.
func (h *handler) injectFontFaces(svg string, cfg config.Config) string {
	dir := h.fontsDir
	if dir == "" {
		dir = "assets/fonts"
	}
	fams := h.svgFontFaces(svg, cfg, dir)
	if len(fams) == 0 {
		return svg
	}
	names := make([]string, 0, len(fams))
	for fam := range fams {
		names = append(names, fam)
	}
	sort.Strings(names)
	var rules strings.Builder
	for _, fam := range names {
		fmt.Fprintf(&rules, `@font-face{font-family:'%s';font-display:swap;src:url('/asset/fonts/%s')}`,
			fam, url.PathEscape(filepath.Base(fams[fam])))
	}
	return spliceStyleTag(svg, rules.String())
}

// svgFontFaces collects family → font file for every family the SVG
// references: configured fontFiles first, then assets/fonts matches.
func (h *handler) svgFontFaces(svg string, cfg config.Config, dir string) map[string]string {
	fams := map[string]string{}
	for _, f := range cfg.Render.FontFiles {
		if filepath.ToSlash(filepath.Dir(f)) != filepath.ToSlash(dir) {
			continue
		}
		fam := fonts.FamilyName(f)
		if fam == "" {
			fam = cfg.Render.FontFamily
		}
		if fam = cssSafe(fam); fam != "" {
			fams[fam] = f
		}
	}
	for _, m := range fontFamilyVal.FindAllStringSubmatch(svg, -1) {
		v := m[1]
		if v == "" {
			v = m[2]
		}
		if v == "" {
			v = m[3]
		}
		for _, fam := range strings.Split(html.UnescapeString(v), ",") {
			fam = cssSafe(strings.TrimSpace(fam))
			if fam == "" || fams[fam] != "" {
				continue
			}
			if p := h.fontIndex(dir)[fam]; p != "" {
				fams[fam] = p
			}
		}
	}
	return fams
}

// spliceStyleTag injects <style>…</style> just inside the root <svg> tag.
func spliceStyleTag(svg, rules string) string {
	i := strings.Index(svg, "<svg")
	if i < 0 {
		return svg
	}
	j := strings.Index(svg[i:], ">")
	if j < 0 {
		return svg
	}
	j += i
	if svg[j-1] == '/' { // self-closing <svg/> — nothing to style
		return svg
	}
	return svg[:j+1] + "<style>" + rules + "</style>" + svg[j+1:]
}

// cssSafe strips characters that could break out of a single-quoted CSS
// declaration — family names and URLs come from font files on disk.
func cssSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`';{}\\`, r) {
			return -1
		}
		return r
	}, s)
}

// fontIndex maps font family → file for everything parseable under dir,
// rebuilt when the directory's mtime changes (fonts are dropped in ad hoc).
func (h *handler) fontIndex(dir string) map[string]string {
	st, err := os.Stat(dir)
	h.fontMu.Lock()
	defer h.fontMu.Unlock()
	if err == nil && h.fontIdx != nil && st.ModTime().Equal(h.fontIdxAt) {
		return h.fontIdx
	}
	idx := fonts.Index(dir)
	h.fontIdx, h.fontIdxAt = idx, time.Time{}
	if err == nil {
		h.fontIdxAt = st.ModTime()
	}
	return idx
}

func (h *handler) renderAll(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		jsonErr(w, err)
		return
	}
	cfg, err := config.Parse(body)
	if err != nil {
		jsonErr(w, err)
		return
	}
	files := make([]string, 0, len(cfg.Output.Formats))
	for _, f := range cfg.Output.Formats {
		if norm := config.NormalizeFormat(f); norm != "" {
			files = append(files, cfg.Name+"/logo."+norm)
		}
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		if err := render.Site(cfg, filepath.Join(h.outDir, cfg.Name)); err != nil {
			jsonErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"files": files})
		return
	}
	// SSE progress: one data: {"file":…} frame per written file, then a
	// final {"done":true,"files":[…]} (or {"error":…}) frame.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusServiceUnavailable)
		return
	}
	send := func(v any) {
		b, _ := json.Marshal(v)
		// nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter -- SSE frame; b is json.Marshal'd, content is text/event-stream
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	if err := render.Site(cfg, filepath.Join(h.outDir, cfg.Name), func(path string) {
		send(map[string]string{"file": filepath.Base(path)})
	}); err != nil {
		send(map[string]string{"error": err.Error()})
		return
	}
	send(map[string]any{"done": true, "files": files})
}

func (h *handler) save(w http.ResponseWriter, r *http.Request) {
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
	if _, err := config.Parse(body); err != nil {
		jsonErr(w, err)
		return
	}
	if err := os.WriteFile(path, body, 0o644); err != nil { // #nosec G306 -- site configs are committed files, not secrets
		jsonErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"saved": path})
}

// duplicate copies configs/<name>.json to configs/<to>.json, rewriting the
// "name" field so the copy is a valid standalone site. `to` must not exist
// yet — duplicates never clobber an existing config.
func (h *handler) duplicate(w http.ResponseWriter, r *http.Request) {
	if err := h.copyConfig(r.URL.Query().Get("name"), r.URL.Query().Get("to")); err != nil {
		jsonErr(w, err)
		return
	}
	dst, _ := h.configPath(r.URL.Query().Get("to"))
	writeJSON(w, http.StatusOK, map[string]string{"saved": dst})
}

// copyConfig writes configs/<to>.json as a copy of configs/<name>.json with
// its "name" field rewritten to <to>. Both names are validated through
// configPath — copies never clobber an existing config.
func (h *handler) copyConfig(name, to string) error {
	src, err := h.configPath(name)
	if err != nil {
		return err
	}
	dst, err := h.configPath(to)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(src) // #nosec G304 -- name is regex-validated, path joins configsPath
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		return fmt.Errorf("%s already exists", filepath.Base(dst))
	}
	enc, _ := json.Marshal(to) // always ValidSiteName here — safe chars
	if nameRe.Match(body) {
		body = nameRe.ReplaceAll(body, []byte(`"name": `+string(enc)))
	} else {
		body = append([]byte(`{"name": `+string(enc)+`,`), body[1:]...)
	}
	if _, err := config.Parse(body); err != nil {
		return fmt.Errorf("copied config would be invalid: %w", err)
	}
	if err := os.WriteFile(dst, body, 0o644); err != nil { // #nosec G306 G703 -- `to` is regex-validated via configPath and dst joins configsPath; site configs are committed files, not secrets
		return err
	}
	return nil
}

// rename moves configs/<name>.json to configs/<to>.json — a copy followed
// by removal of the source.
func (h *handler) rename(w http.ResponseWriter, r *http.Request) {
	name, to := r.URL.Query().Get("name"), r.URL.Query().Get("to")
	if name == to {
		jsonErr(w, fmt.Errorf("nothing to rename: %s", name))
		return
	}
	src, err := h.configPath(name)
	if err != nil {
		jsonErr(w, err)
		return
	}
	if err := h.copyConfig(name, to); err != nil {
		jsonErr(w, err)
		return
	}
	// #nosec G703 -- src is regex-validated via configPath and joins configsPath.
	if err := os.Remove(src); err != nil {
		jsonErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"saved": r.URL.Query().Get("to")})
}

// deleteConfig removes configs/<name>.json. The last remaining config is
// refused — the editor needs at least one site.
func (h *handler) deleteConfig(w http.ResponseWriter, r *http.Request) {
	p, err := h.configPath(r.URL.Query().Get("name"))
	if err != nil {
		jsonErr(w, err)
		return
	}
	if entries, derr := filepath.Glob(filepath.Join(h.configsPath, "*.json")); derr == nil && len(entries) <= 1 {
		jsonErr(w, fmt.Errorf("refusing to delete the last config"))
		return
	}
	// #nosec G703 -- p is regex-validated via configPath and joins configsPath.
	if err := os.Remove(p); err != nil {
		jsonErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": filepath.Base(p)})
}

// nameRe targets the top-level "name" field for duplicate's rewrite.
var nameRe = regexp.MustCompile(`"name"\s*:\s*"([^"\\]|\\.)*"`)

// assetList serves the contents of an assets subdirectory as
// {key: [{name, path, svg}]}; a missing dir yields an empty list.
func (h *handler) assetList(dir, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		items := []map[string]any{}
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				it := map[string]any{
					"name": e.Name(),
					"path": filepath.ToSlash(filepath.Join(dir, e.Name())),
					"svg":  strings.HasSuffix(strings.ToLower(e.Name()), ".svg"),
				}
				if fam := fonts.FamilyName(filepath.Join(dir, e.Name())); fam != "" {
					it["family"] = fam
				}
				items = append(items, it)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{key: items})
	}
}

// anchoredPath cleans a request path so ".." can never escape the serve
// root: anchoring at "/" makes traversal collapse to the root, then the
// leading slash is trimmed for a safe filepath.Join.
func anchoredPath(p string) string {
	return filepath.FromSlash(strings.TrimPrefix(
		filepath.Clean("/"+strings.Trim(filepath.FromSlash(p), "/")), "/"))
}

// assetFile serves files under assets/images or assets/fonts for previews.
func (h *handler) assetFile(w http.ResponseWriter, r *http.Request) {
	rel := anchoredPath(strings.TrimPrefix(r.URL.Path, "/asset/"))
	sub := strings.SplitN(rel, "/", 2)
	if len(sub) != 2 || (sub[0] != "images" && sub[0] != "fonts") {
		jsonErr(w, fmt.Errorf("invalid asset path"))
		return
	}
	// Content assets (fonts/images) change rarely — an hour of caching
	// skips revalidation of multi-hundred-KB files while bounding staleness.
	w.Header().Set("Cache-Control", "max-age=3600")
	http.ServeFile(w, r, filepath.Join("assets", rel)) // #nosec G703 -- path is root-anchored Clean + restricted to images|fonts
}

func (h *handler) outputFile(w http.ResponseWriter, r *http.Request) {
	rel := anchoredPath(strings.TrimPrefix(r.URL.Path, "/out/"))
	http.ServeFile(w, r, filepath.Join(h.outDir, rel)) // #nosec G703 -- path is root-anchored Clean, cannot escape outDir
}

// ---- template analysis & placeholder fill (editor "map template" flow) ----

// tplElement is one SVG element that can act as a replacement region. Elements
// without an id get a synthetic "auto-<ord>" id — filled into the file by
// templateFill so the result is stable afterwards. Fp fingerprints the element
// (tag + normalized open tag + text) so templateFill can detect a template that
// changed since the map modal scanned it — ordinals are only valid for the
// file they were scanned from.
type tplElement struct {
	ID   string  `json:"id"`
	Tag  string  `json:"tag"`
	Slot string  `json:"slot,omitempty"`
	Ord  int     `json:"ord"`
	Fp   string  `json:"fp,omitempty"`
	Text string  `json:"text,omitempty"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`

	// Loc/Inner index into the scanned document — only used to splice
	// auto-* elements during templateFill; never serialized.
	Loc   [2]int `json:"-"`
	Inner [2]int `json:"-"`
}

var (
	tplElemRe   = regexp.MustCompile(`(?s)<(path|rect|image|circle|ellipse|polygon|line)\b[^>]*>|<text\b[^>]*>(.*?)</text>`)
	tplIDRe     = regexp.MustCompile(`\sid="([^"]+)"`)
	tplPointsRe = regexp.MustCompile(`\spoints="([^"]*)"`)
	tplDRe      = regexp.MustCompile(`\bd="([^"]*)"`)
	tplNumRe    = regexp.MustCompile(`-?\d+\.?\d*`)
	// Attr names are preceded by whitespace so stroke-width= cannot alias
	// width= (a `-` is not a word boundary we want to match on).
	tplAttrRe  = regexp.MustCompile(`\s(x|y|cx|cy|r|rx|ry|x1|y1|x2|y2|width|height)="(-?\d+\.?\d*)"`)
	tplXYRe    = regexp.MustCompile(`matrix\(1 0 0 1 (-?\d+\.?\d*) (-?\d+\.?\d*)\)`)
	tplTransRe = regexp.MustCompile(
		`translate\(\s*(-?\d+\.?\d*)[\s,]+(-?\d+\.?\d*)`)
	tplFontSizeRe = regexp.MustCompile(`\bfont-size\s*[:=]\s*"?(-?\d+\.?\d*)`)
	tplAnchorRe   = regexp.MustCompile(`\btext-anchor="(middle|end)"`)
	// Comments, <style> and <defs> are blanked (length-preserving) before
	// scanning so markup inside them can't register as mappable elements —
	// offsets stay valid for splicing into the original document.
	tplSkipRe  = regexp.MustCompile(`(?s)<!--.*?-->|<style\b[^>]*>.*?</style>|<defs\b[^>]*>.*?</defs>`)
	tplInnerRe = regexp.MustCompile(`<[^>]*>`)
	tplEntRe   = regexp.MustCompile(`&[#a-zA-Z0-9]+;`)
	tplVBRe    = regexp.MustCompile(`viewBox="(-?[\d.]+)[ ,]+(-?[\d.]+)[ ,]+([\d.]+)[ ,]+([\d.]+)"`)
)

// tplFingerprint hashes an element's identity — tag + whitespace-normalized
// open tag + text content — so templateFill can tell "same element" from
// "template was edited since the map modal scanned it".
func tplFingerprint(tag, openTag, text string) string {
	norm := strings.Join(strings.Fields(openTag), " ")
	norm = strings.ReplaceAll(norm, "= ", "=")
	norm = strings.ReplaceAll(norm, " =", "=")
	sum := sha256.Sum256([]byte(tag + "|" + norm + "|" + text))
	return hex.EncodeToString(sum[:])[:12]
}

// analyzeTemplate finds mappable elements and their bounding boxes. Elements
// without an id are numbered auto-<ord> by document order. For <path> and
// <polygon> the box is min/max over coordinate pairs — exact for absolute
// outlines like the figure boxes, approximate otherwise.
func analyzeTemplate(svg string) []tplElement {
	svg = tplSkipRe.ReplaceAllStringFunc(svg, func(m string) string {
		return strings.Repeat(" ", len(m))
	})
	els := []tplElement{}
	for i, m := range tplElemRe.FindAllStringSubmatchIndex(svg, -1) {
		match := svg[m[0]:m[1]]
		e := tplElement{Ord: i, Loc: [2]int{m[0], m[1]}}
		if m[2] >= 0 {
			e.Tag = svg[m[2]:m[3]]
		} else {
			e.Tag = "text"
			e.Inner = [2]int{m[4], m[5]}
		}
		openTag := match
		if j := strings.IndexByte(openTag, '>'); j >= 0 {
			openTag = openTag[:j]
		}
		if idm := tplIDRe.FindStringSubmatch(openTag); idm != nil {
			e.ID = idm[1]
		} else {
			e.ID = fmt.Sprintf("auto-%d", e.Ord)
		}
		if e.Tag == "text" {
			e.Text = tplTextLabel(svg[e.Inner[0]:e.Inner[1]])
		}
		e.Fp = tplFingerprint(e.Tag, openTag, e.Text)
		if p := config.Placeholder.FindStringSubmatch(match); p != nil {
			e.Slot = p[1]
		}
		tplBBox(&e, match, openTag)
		els = append(els, e)
	}
	return els
}

// tplTextLabel extracts a short human-readable label from a <text>
// element's inner markup for the map-template list.
func tplTextLabel(inner string) string {
	inner = tplEntRe.ReplaceAllString(tplInnerRe.ReplaceAllString(inner, ""), "x")
	s := strings.Join(strings.Fields(inner), " ")
	if utf8.RuneCountInString(s) > 40 {
		s = string([]rune(s)[:40])
	}
	return s
}

// tplBBox fills e's bounding box according to its tag kind.
func tplBBox(e *tplElement, match, openTag string) {
	switch e.Tag {
	case "path", "polygon":
		coordPairBBox(e, match)
	case "text":
		textBBox(e, match, openTag)
	case "circle":
		attrs := tplAttrs(match)
		e.X, e.Y = attrs["cx"]-attrs["r"], attrs["cy"]-attrs["r"]
		e.W, e.H = 2*attrs["r"], 2*attrs["r"]
	case "ellipse":
		attrs := tplAttrs(match)
		e.X, e.Y = attrs["cx"]-attrs["rx"], attrs["cy"]-attrs["ry"]
		e.W, e.H = 2*attrs["rx"], 2*attrs["ry"]
	case "line":
		attrs := tplAttrs(match)
		e.X, e.W = min(attrs["x1"], attrs["x2"]), max(math.Abs(attrs["x2"]-attrs["x1"]), 1)
		e.Y, e.H = min(attrs["y1"], attrs["y2"]), max(math.Abs(attrs["y2"]-attrs["y1"]), 1)
	default:
		attrs := tplAttrs(match)
		e.X, e.Y, e.W, e.H = attrs["x"], attrs["y"], attrs["width"], attrs["height"]
	}
}

// coordPairBBox boxes a path/polygon as min/max over coordinate pairs.
func coordPairBBox(e *tplElement, match string) {
	var nums []string
	if e.Tag == "polygon" {
		if pm := tplPointsRe.FindStringSubmatch(match); pm != nil {
			nums = tplNumRe.FindAllString(pm[1], -1)
		}
	} else if dm := tplDRe.FindStringSubmatch(match); dm != nil {
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
}

// textBBox estimates a <text> box (no renderer exists): position from
// matrix()/translate()/x y attrs, size from font-size x text length. The
// y attr is the baseline, so the box extends above it.
func textBBox(e *tplElement, match, openTag string) {
	if t := tplXYRe.FindStringSubmatch(match); t != nil {
		e.X, e.Y = atof(t[1]), atof(t[2])
	} else if t := tplTransRe.FindStringSubmatch(match); t != nil {
		e.X, e.Y = atof(t[1]), atof(t[2])
	} else {
		attrs := tplAttrs(openTag)
		e.X, e.Y = attrs["x"], attrs["y"]
	}
	size := 24.0
	if fs := tplFontSizeRe.FindStringSubmatch(match); fs != nil {
		size = atof(fs[1])
	}
	e.W = float64(utf8.RuneCountInString(e.Text)) * size * 0.6
	e.W = max(e.W, size)
	e.H = size * 1.2
	if a := tplAnchorRe.FindStringSubmatch(match); a != nil {
		if a[1] == "middle" {
			e.X -= e.W / 2
		} else {
			e.X -= e.W
		}
	}
	e.Y -= size * 0.9
}

func tplAttrs(tag string) map[string]float64 {
	attrs := map[string]float64{}
	for _, a := range tplAttrRe.FindAllStringSubmatch(tag, -1) {
		attrs[a[1]] = atof(a[2])
	}
	return attrs
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
//
// Elements addressed by a synthetic auto-<ord> id are located by their
// document-order ordinal and verified against fps (fingerprint from the
// analyze pass that produced the mapping) — a template that changed since
// mapping fails loudly rather than writing the placeholder into the wrong
// element. Filled elements get a real id="auto-N" injected, so the output
// re-analyzes deterministically.
func templateFill(svg string, mapping, fps map[string]string) (string, error) {
	byID := map[string][]string{}
	for slot, id := range mapping {
		byID[id] = append(byID[id], slot)
	}
	targets := []tplElement{}
	seen := map[string]bool{}
	for _, e := range analyzeTemplate(svg) {
		if _, ok := byID[e.ID]; !ok || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		if want := fps[e.ID]; want != "" && want != e.Fp {
			return "", fmt.Errorf("element %s changed since mapping — reopen map template to remap", e.ID)
		}
		targets = append(targets, e)
	}
	for id := range byID {
		if !seen[id] {
			return "", fmt.Errorf("element id %q not found in template", id)
		}
	}
	// Splice right-to-left so earlier offsets stay valid.
	sort.Slice(targets, func(i, j int) bool { return targets[i].Loc[0] > targets[j].Loc[0] })
	for _, e := range targets {
		slots := byID[e.ID]
		sort.Strings(slots)
		if e.Tag == "text" {
			if strings.HasPrefix(e.ID, "auto-") {
				openTag := svg[e.Loc[0]:e.Loc[1]]
				openTag = openTag[:strings.IndexByte(openTag, '>')] + ` id="` + e.ID + `">`
				svg = svg[:e.Loc[0]] + openTag + fillTextInner(svg[e.Inner[0]:e.Inner[1]], slots) + "</text>" + svg[e.Loc[1]:]
			} else {
				svg = svg[:e.Inner[0]] + fillTextInner(svg[e.Inner[0]:e.Inner[1]], slots) + svg[e.Inner[1]:]
			}
			continue
		}
		img := fmt.Sprintf(`<image id="%s" href="{{%s}}" x="%v" y="%v" width="%v" height="%v" preserveAspectRatio="xMidYMid meet"/>`,
			e.ID, slots[0], e.X, e.Y, e.W, e.H)
		end := e.Loc[1]
		if !strings.HasSuffix(strings.TrimSpace(svg[e.Loc[0]:e.Loc[1]]), "/>") {
			if closeTag := "</" + e.Tag + ">"; strings.HasPrefix(svg[end:], closeTag) {
				end += len(closeTag)
			}
		}
		svg = svg[:e.Loc[0]] + img + svg[end:]
	}
	return svg, nil
}

// fillTextInner distributes slots across a <text> element's tspans (emptied
// when unmapped). A <text> without tspans gets the joined placeholders as
// plain content.
func fillTextInner(inner string, slots []string) string {
	tspans := regexp.MustCompile(`(?s)<tspan\b[^>]*>.*?</tspan>`).FindAllString(inner, -1)
	if len(tspans) == 0 {
		extra := ""
		for _, s := range slots {
			extra += "{{" + s + "}}"
		}
		return extra
	}
	filled := []string{}
	for i, t := range tspans {
		open := t[:strings.Index(t, ">")+1]
		if i < len(slots) {
			filled = append(filled, open+"{{"+slots[i]+"}}</tspan>")
		} else {
			filled = append(filled, open+"</tspan>")
		}
	}
	if len(slots) > len(tspans) {
		extra := ""
		for _, s := range slots[len(tspans):] {
			extra += "{{" + s + "}}"
		}
		filled[len(filled)-1] = strings.TrimSuffix(filled[len(filled)-1], "</tspan>") + extra + "</tspan>"
	}
	return strings.Join(filled, "")
}

func (h *handler) tplAnalyze(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		jsonErr(w, err)
		return
	}
	var cfg config.Config
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
		"svg":      string(b),
	})
}

type tplFillReq struct {
	Config  json.RawMessage   `json:"config"`
	Mapping map[string]string `json:"mapping"`
	// FPs carries element fingerprints from the template-analyze pass that
	// produced the mapping — guards auto-* ordinals against template drift.
	FPs map[string]string `json:"fps,omitempty"`
	Out string            `json:"out"`
}

func (h *handler) tplFill(w http.ResponseWriter, r *http.Request) {
	var req tplFillReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		jsonErr(w, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	cfg, err := config.Parse(req.Config)
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
	filled, err := templateFill(string(b), req.Mapping, req.FPs)
	if err != nil {
		jsonErr(w, err)
		return
	}
	dst := strings.TrimSuffix(strings.TrimSuffix(cfg.Template, filepath.Ext(cfg.Template)), "-filled") + "-filled.svg"
	if req.Out != "" {
		if !strings.HasPrefix(req.Out, "assets/templates/") || strings.Contains(req.Out, "..") || !strings.HasSuffix(req.Out, ".svg") {
			jsonErr(w, fmt.Errorf("out must stay under assets/templates/ and end in .svg"))
			return
		}
		dst = req.Out
	}
	// #nosec G306 G703 -- dst is constrained to assets/templates/*.svg or derived from the config template path.
	if err := os.WriteFile(dst, []byte(filled), 0o644); err != nil {
		jsonErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"template": dst})
}

func templateFillValidate(mapping map[string]string) error {
	for slot := range mapping {
		if !config.ValidTextSlot(slot) && !config.ValidImageSlot(slot) {
			return fmt.Errorf("invalid slot %q", slot)
		}
	}
	return nil
}

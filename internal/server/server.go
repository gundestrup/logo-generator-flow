// Package server hosts the loopback browser editor (-ui): config CRUD,
// live SVG preview, template mapping, and on-demand raster renders.
package server

import (
	"context"
	"embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
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

	"example.com/logo-generator/internal/config"
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
	mux.HandleFunc("GET /asset/", h.assetFile)
	mux.HandleFunc("GET /out/", h.outputFile)
	mux.HandleFunc("POST /api/template-analyze", h.tplAnalyze)
	mux.HandleFunc("POST /api/template-fill", h.tplFill)
	mux.HandleFunc("POST /api/preview", h.preview)
	mux.HandleFunc("POST /api/render", h.renderAll)
	mux.HandleFunc("POST /api/save", h.save)
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
	_, _ = w.Write(b)
}

// cacheStatic marks embedded UI assets as cacheable for the editor session;
// they only change with a new binary build.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func (h *handler) favicon(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16" rx="4" fill="#0b1220"/><path d="M4 8h8M8 4v8" stroke="#38bdf8" stroke-width="2" stroke-linecap="round"/></svg>`))
}

func (h *handler) sites(w http.ResponseWriter, _ *http.Request) {
	files, err := config.Files(h.configsPath)
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

// injectFontFaces adds <style>@font-face</style> rules covering every
// font-family the SVG references — configured files plus anything matching
// an assets/fonts file — so the browser preview renders custom fonts.
// Rasters don't need it: resvg loads the same files via --use-font-file.
func (h *handler) injectFontFaces(svg string, cfg config.Config) string {
	dir := h.fontsDir
	if dir == "" {
		dir = "assets/fonts"
	}
	fams := map[string]string{} // family → font file
	for _, f := range cfg.Render.FontFiles {
		if filepath.ToSlash(filepath.Dir(f)) != filepath.ToSlash(dir) {
			continue
		}
		fam := fontFamilyName(f)
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
	return svg[:j+1] + "<style>" + rules.String() + "</style>" + svg[j+1:]
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
	idx := map[string]string{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if fam := fontFamilyName(p); fam != "" {
				idx[fam] = filepath.ToSlash(p)
			}
		}
	}
	h.fontIdx, h.fontIdxAt = idx, time.Time{}
	if err == nil {
		h.fontIdxAt = st.ModTime()
	}
	return idx
}

// fontFamilyName reads the family name out of a TTF/OTF/TTC sfnt name
// table (nameID 16 typographic family, else nameID 1). Returns "" for
// formats it can't parse (WOFF/WOFF2) so callers can fall back.
func fontFamilyName(path string) string {
	f, err := os.Open(path) // #nosec G304 -- font files under assets/fonts or from config
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 8<<20))
	if err != nil || len(b) < 16 {
		return ""
	}
	off := 0
	if string(b[:4]) == "ttcf" {
		off = int(binary.BigEndian.Uint32(b[12:16]))
	}
	if off+6 > len(b) {
		return ""
	}
	numTables := int(binary.BigEndian.Uint16(b[off+4:]))
	for i := 0; i < numTables; i++ {
		rec := off + 12 + 16*i
		if rec+16 > len(b) {
			break
		}
		if string(b[rec:rec+4]) != "name" {
			continue
		}
		toff := int(binary.BigEndian.Uint32(b[rec+8:]))
		if toff+6 > len(b) {
			return ""
		}
		count := int(binary.BigEndian.Uint16(b[toff+2:]))
		strOff := toff + int(binary.BigEndian.Uint16(b[toff+4:]))
		var fam16, fam1 string
		for j := 0; j < count; j++ {
			nr := toff + 6 + 12*j
			if nr+12 > len(b) {
				break
			}
			nid := binary.BigEndian.Uint16(b[nr+6:])
			nlen := int(binary.BigEndian.Uint16(b[nr+8:]))
			noff := strOff + int(binary.BigEndian.Uint16(b[nr+10:]))
			if noff < 0 || noff+nlen > len(b) {
				continue
			}
			if s := nameString(b[nr:nr+4], b[noff:noff+nlen]); s != "" {
				if nid == 16 && fam16 == "" {
					fam16 = s
				}
				if nid == 1 && fam1 == "" {
					fam1 = s
				}
			}
		}
		if fam16 != "" {
			return fam16
		}
		return fam1
	}
	return ""
}

// nameString decodes a name-table record per its platform: Windows (3) and
// Unicode (0) store UTF-16BE; Macintosh (1) is ASCII-compatible.
func nameString(platform []byte, raw []byte) string {
	pid := binary.BigEndian.Uint16(platform[:2])
	if pid == 3 || pid == 0 {
		rs := make([]rune, 0, len(raw)/2)
		for i := 0; i+2 <= len(raw); i += 2 {
			rs = append(rs, rune(binary.BigEndian.Uint16(raw[i:])))
		}
		return string(rs)
	}
	if pid == 1 {
		return string(raw)
	}
	return ""
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
				if fam := fontFamilyName(filepath.Join(dir, e.Name())); fam != "" {
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
	// Short cache: skips per-load revalidation of multi-hundred-KB font
	// files; capped so a replaced asset can't look stale for long.
	w.Header().Set("Cache-Control", "max-age=300")
	http.ServeFile(w, r, filepath.Join("assets", rel)) // #nosec G703 -- path is root-anchored Clean + restricted to images|fonts
}

func (h *handler) outputFile(w http.ResponseWriter, r *http.Request) {
	rel := anchoredPath(strings.TrimPrefix(r.URL.Path, "/out/"))
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
		if p := config.Placeholder.FindStringSubmatch(m[0]); p != nil {
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
	})
}

type tplFillReq struct {
	Config  json.RawMessage   `json:"config"`
	Mapping map[string]string `json:"mapping"`
	Out     string            `json:"out"`
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
	filled, err := templateFill(string(b), req.Mapping)
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

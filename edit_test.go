package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const editTestTemplate = `<svg xmlns="http://www.w3.org/2000/svg"><text>{{text1}}</text></svg>`

const editTestConfig = `{
  "name": "acme",
  "template": "templates/logo.svg",
  "output": {"formats": ["svg"]},
  "text": {"text1": "Acme Co", "text2": "", "text3": "", "text4": ""}
}`

func newEditTestHandler(t *testing.T) *editHandler {
	t.Helper()
	root := t.TempDir()
	configs := filepath.Join(root, "configs")
	mustMkdir(t, filepath.Join(root, "templates"))
	mustMkdir(t, configs)
	// #nosec G306 -- test fixture file
	if err := os.WriteFile(filepath.Join(configs, "acme.json"), []byte(editTestConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	// #nosec G306 -- test fixture file
	if err := os.WriteFile(filepath.Join(root, "templates", "logo.svg"), []byte(editTestTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return &editHandler{configsPath: configs, outDir: filepath.Join(root, "output")}
}

// doEdit drives requests through the real registered mux so route drift
// between production and tests can't go unnoticed.
func doEdit(h *editHandler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	w := httptest.NewRecorder()
	editMux(h, func() {}).ServeHTTP(w, req)
	return w
}

func TestEditSites(t *testing.T) {
	h := newEditTestHandler(t)
	w := doEdit(h, "GET", "/api/sites", "")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var res struct {
		Sites []string `json:"sites"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Sites) != 1 || res.Sites[0] != "acme" {
		t.Fatalf("sites = %v", res.Sites)
	}
}

func TestEditSite(t *testing.T) {
	h := newEditTestHandler(t)
	w := doEdit(h, "GET", "/api/site?name=acme", "")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var res struct {
		Config   string `json:"config"`
		Template string `json:"template"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Config, `"name": "acme"`) || !strings.Contains(res.Template, "{{text1}}") {
		t.Fatal("site response missing config or template")
	}
}

func TestEditSiteBadName(t *testing.T) {
	h := newEditTestHandler(t)
	if w := doEdit(h, "GET", "/api/site?name=../x", ""); w.Code != 400 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestEditPreview(t *testing.T) {
	h := newEditTestHandler(t)
	w := doEdit(h, "POST", "/api/preview", strings.ReplaceAll(editTestConfig, "Acme Co", "New Name"))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var res struct {
		SVG string `json:"svg"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.SVG, "New Name") {
		t.Fatalf("preview missing edited text: %s", res.SVG)
	}
}

func TestEditPreviewErrors(t *testing.T) {
	h := newEditTestHandler(t)
	for _, body := range []string{`{`, `{"name":"BAD!"}`, `{"name":"acme","unknown":1}`} {
		if w := doEdit(h, "POST", "/api/preview", body); w.Code != 400 {
			t.Fatalf("body %q: status %d", body, w.Code)
		}
	}
}

func TestEditSave(t *testing.T) {
	h := newEditTestHandler(t)
	w := doEdit(h, "POST", "/api/save?name=acme", editTestConfig)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	b, err := os.ReadFile(filepath.Join(h.configsPath, "acme.json"))
	if err != nil || !strings.Contains(string(b), "Acme Co") {
		t.Fatalf("saved file: %v %q", err, b)
	}
}

func TestEditSaveRejectsInvalid(t *testing.T) {
	h := newEditTestHandler(t)
	if w := doEdit(h, "POST", "/api/save?name=bad%2Fname", editTestConfig); w.Code != 400 {
		t.Fatalf("bad name: status %d", w.Code)
	}
	if w := doEdit(h, "POST", "/api/save?name=acme", `{`); w.Code != 400 {
		t.Fatalf("bad json: status %d", w.Code)
	}
}

func TestEditRenderAll(t *testing.T) {
	h := newEditTestHandler(t)
	w := doEdit(h, "POST", "/api/render", editTestConfig)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	out := filepath.Join(h.outDir, "acme", "logo.svg")
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected %s", out)
	}
}

func TestOutputFile(t *testing.T) {
	h := newEditTestHandler(t)
	// #nosec G301 -- test fixture in a private t.TempDir().
	mustMkdir(t, filepath.Join(h.outDir, "acme"))
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(filepath.Join(h.outDir, "acme", "logo.svg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := doEdit(h, "GET", "/out/acme/logo.svg", ""); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if w := doEdit(h, "GET", "/out/../configs/acme.json", ""); w.Code == 200 {
		t.Fatal("traversal was served")
	}
}

func TestEditAssets(t *testing.T) {
	h := newEditTestHandler(t)
	// #nosec G301 -- test fixture dir
	mustMkdir(t, "assets/images")
	// #nosec G306 -- test fixture file
	if err := os.WriteFile("assets/images/badge.svg", []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := doEdit(h, "GET", "/api/images", "")
	var res struct {
		Images []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			SVG  bool   `json:"svg"`
		} `json:"images"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || res.Images[0].Name != "badge.svg" || !res.Images[0].SVG {
		t.Fatalf("images = %+v", res.Images)
	}
	// fonts dir missing → empty list, not an error
	w = doEdit(h, "GET", "/api/fonts", "")
	if w.Code != 200 {
		t.Fatalf("fonts status %d", w.Code)
	}
	// asset file serves + rejects non-asset paths
	if w := doEdit(h, "GET", "/asset/images/badge.svg", ""); w.Code != 200 {
		t.Fatalf("asset status %d", w.Code)
	}
	if w := doEdit(h, "GET", "/asset/../configs/acme.json", ""); w.Code == 200 {
		t.Fatal("asset traversal was served")
	}
}

func TestEditPreviewObjectSlots(t *testing.T) {
	h := newEditTestHandler(t)
	cfg := `{"name":"acme","template":"templates/logo.svg","output":{"formats":["svg"]},
		"text":{"text1":{"text":"Styled","color":"#ff0000"},"text2":"","text3":"","text4":""}}`
	w := doEdit(h, "POST", "/api/preview", cfg)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var res struct {
		SVG string `json:"svg"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.SVG, `<tspan fill="#ff0000">Styled</tspan>`) {
		t.Fatalf("missing tspan: %s", res.SVG)
	}
}

func TestAnalyzeTemplate(t *testing.T) {
	svg := `<svg><g><path id="Box" d="M10 20 L60 20 C65 20 70 25 70 30 L70 80 L10 80 Z"/><text id="Lbl" transform="matrix(1 0 0 1 100 200)"><tspan>x</tspan></text><path d="M0 0"/></g></svg>`
	els := analyzeTemplate(svg)
	if len(els) != 2 {
		t.Fatalf("elements = %+v", els)
	}
	if els[0].ID != "Box" || els[0].X != 10 || els[0].Y != 20 || els[0].W != 60 || els[0].H != 60 {
		t.Fatalf("box = %+v", els[0])
	}
	if els[1].ID != "Lbl" || els[1].X != 100 || els[1].Y != 200 {
		t.Fatalf("text = %+v", els[1])
	}
}

func TestTemplateFill(t *testing.T) {
	svg := `<svg><path id="Box" d="M10 20 L70 20 L70 80 L10 80 Z" class="c"/><text id="Lbl"><tspan class="a">one</tspan><tspan class="b">two</tspan><tspan class="c">three</tspan></text></svg>`
	out, err := templateFill(svg, map[string]string{"image1": "Box", "text1": "Lbl", "text2": "Lbl"})
	if err != nil {
		t.Fatal(err)
	}
	want := `<image id="Box" href="{{image1}}" x="10" y="20" width="60" height="60" preserveAspectRatio="xMidYMid meet"/>`
	if !strings.Contains(out, want) {
		t.Fatalf("missing image: %s", out)
	}
	if !strings.Contains(out, `<tspan class="a">{{text1}}</tspan>`) ||
		!strings.Contains(out, `<tspan class="b">{{text2}}</tspan>`) ||
		!strings.Contains(out, `<tspan class="c"></tspan>`) {
		t.Fatalf("tspans: %s", out)
	}
	if _, err := templateFill(svg, map[string]string{"image1": "Nope"}); err == nil {
		t.Fatal("expected missing-id error")
	}
}

func TestEditPing(t *testing.T) {
	h := &editHandler{configsPath: t.TempDir(), outDir: t.TempDir()}
	w := doEdit(h, "GET", "/api/ping", "")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var p struct {
		App string `json:"app"`
		PID int    `json:"pid"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.App != "logo-generator" || p.PID <= 0 {
		t.Fatalf("unexpected ping: %+v", p)
	}
}

func TestStopStaleEditor(t *testing.T) {
	var gotShutdown bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ping":
			writeJSON(w, 200, map[string]any{"app": "logo-generator", "pid": 123})
		case "/api/shutdown":
			gotShutdown = true
			writeJSON(w, 200, map[string]bool{"ok": true})
		}
	}))
	defer srv.Close()
	if !stopStaleEditor(strings.TrimPrefix(srv.URL, "http://")) {
		t.Fatal("expected stale editor to be stopped")
	}
	if !gotShutdown {
		t.Fatal("shutdown endpoint was never called")
	}
}

func TestStopStaleEditorForeign(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"app": "other"})
	}))
	defer srv.Close()
	if stopStaleEditor(strings.TrimPrefix(srv.URL, "http://")) {
		t.Fatal("foreign process must not be stopped")
	}
	// Nothing listening at all.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	if stopStaleEditor(addr) {
		t.Fatal("dead port must not report success")
	}
}

func TestEditIndex(t *testing.T) {
	h := &editHandler{configsPath: t.TempDir(), outDir: t.TempDir()}
	w := doEdit(h, "GET", "/", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("index status %d", w.Code)
	}
}

func TestTplAnalyze(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "t.svg")
	svg := `<svg viewBox="0 0 10 20"><rect id="Box" x="1" y="2" width="3" height="4"/><text id="T">{{text1}}</text></svg>`
	if err := os.WriteFile(tpl, []byte(svg), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &editHandler{configsPath: dir, outDir: dir}
	body, _ := json.Marshal(map[string]string{"template": tpl})
	w := doEdit(h, "POST", "/api/template-analyze", string(body))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var res struct {
		Elements []tplElement `json:"elements"`
		ViewBox  []string     `json:"viewBox"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Elements) != 2 || res.Elements[0].ID != "Box" {
		t.Fatalf("unexpected elements: %+v", res.Elements)
	}
	if len(res.ViewBox) != 4 || res.ViewBox[3] != "20" {
		t.Fatalf("viewBox %+v", res.ViewBox)
	}
	// Missing template path → 400.
	w = doEdit(h, "POST", "/api/template-analyze", `{"template":""}`)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestTplFill(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "t.svg")
	svg := `<svg><rect id="Box" x="0" y="0" width="10" height="10"/></svg>`
	if err := os.WriteFile(tpl, []byte(svg), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &editHandler{configsPath: dir, outDir: dir}
	cfg := `{"name":"s","template":"` + tpl + `","output":{"formats":["svg"]}}`
	body, _ := json.Marshal(tplFillReq{
		Config:  json.RawMessage(cfg),
		Mapping: map[string]string{"image1": "Box"},
	})
	w := doEdit(h, "POST", "/api/template-fill", string(body))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	filled, err := os.ReadFile(filepath.Join(dir, "t-filled.svg")) // #nosec G304 -- fixed name inside t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(filled), `{{image1}}`) {
		t.Fatalf("filled template missing slot: %s", filled)
	}
	// Invalid slot name → 400.
	body, _ = json.Marshal(tplFillReq{Config: json.RawMessage(cfg), Mapping: map[string]string{"image9": "Box"}})
	if w = doEdit(h, "POST", "/api/template-fill", string(body)); w.Code != 400 {
		t.Fatalf("invalid slot: expected 400, got %d", w.Code)
	}
	// Out path escaping templates/ → 400.
	body, _ = json.Marshal(tplFillReq{Config: json.RawMessage(cfg), Mapping: map[string]string{"image1": "Box"}, Out: "../x.svg"})
	if w = doEdit(h, "POST", "/api/template-fill", string(body)); w.Code != 400 {
		t.Fatalf("bad out: expected 400, got %d", w.Code)
	}
}

func TestTemplateFillValidate(t *testing.T) {
	if err := templateFillValidate(map[string]string{"image1": "a", "text6": "b"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"image7", "text0", "foo", "Image1"} {
		if templateFillValidate(map[string]string{bad: "x"}) == nil {
			t.Fatalf("slot %q should be rejected", bad)
		}
	}
}

func TestListenWhenFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	got, err := listenWhenFree(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = got.Close()
	// Held port → times out.
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if _, err := listenWhenFree(held.Addr().String(), 200*time.Millisecond); err == nil {
		t.Fatal("expected timeout error for held port")
	}
}

// makeTestFont builds a minimal sfnt binary (TTF) carrying one Windows
// name record so fontFamilyName has a deterministic fixture.
func makeTestFont(t *testing.T, dir, name, family string) string {
	t.Helper()
	raw := []byte(family)
	utf := make([]byte, 0, len(raw)*2)
	for _, r := range family {
		// #nosec G115 -- fixture family is ASCII, r < 0x10000 by construction.
		utf = append(utf, byte(r>>8), byte(r))
	}
	nt := []byte{0, 0, 0, 1, 0, 18}         // format, count=1, stringOffset=18
	nt = append(nt, 0, 3, 0, 1, 4, 9, 0, 1) // platform 3, enc 1, lang 0x0409, nameID 1
	// #nosec G115 -- fixture strings stay under 256 bytes.
	nt = append(nt, byte(len(utf)>>8), byte(len(utf)), 0, 0)
	nt = append(nt, utf...)
	hdr := []byte{0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0} // sfnt 1.0, 1 table
	// #nosec G115 -- fixture table stays under 64KiB.
	rec := append([]byte("name"), 0, 0, 0, 0, 0, 0, 0, 28, 0, 0, byte(len(nt)>>8), byte(len(nt)))
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, append(append(hdr, rec...), nt...), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFontFamilyName(t *testing.T) {
	dir := t.TempDir()
	if got := fontFamilyName(makeTestFont(t, dir, "x.ttf", "TestFam")); got != "TestFam" {
		t.Fatalf("got %q", got)
	}
	bad := filepath.Join(dir, "bad.ttf")
	if err := os.WriteFile(bad, []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := fontFamilyName(bad); got != "" {
		t.Fatalf("garbage font should give empty family, got %q", got)
	}
}

func TestPreviewFontFaces(t *testing.T) {
	h := &editHandler{configsPath: t.TempDir(), outDir: t.TempDir()}
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><text>hi</text></svg>`
	cfg := Config{}
	cfg.Render.FontFamily = "Fam"
	cfg.Render.FontFiles = []string{"assets/fonts/x.ttf"}
	out := h.injectFontFaces(svg, cfg)
	if !strings.Contains(out, `<style>@font-face`) || !strings.Contains(out, `/asset/fonts/x.ttf`) {
		t.Fatalf("missing @font-face: %s", out)
	}
	if !strings.Contains(out, `font-family:'Fam'`) {
		t.Fatalf("missing family: %s", out)
	}
	// Fonts outside assets/fonts are not servable → skipped.
	cfg.Render.FontFiles = []string{"/elsewhere/x.ttf"}
	if out := h.injectFontFaces(svg, cfg); strings.Contains(out, "@font-face") {
		t.Fatalf("outside font should be skipped: %s", out)
	}
	// Self-closing <svg/> must not be modified.
	if out := h.injectFontFaces(`<svg xmlns="http://www.w3.org/2000/svg"/>`, cfg); strings.Contains(out, "<style>") {
		t.Fatalf("self-closing svg modified: %s", out)
	}
}

func TestFontIndexAndMarkupFaces(t *testing.T) {
	dir := t.TempDir()
	makeTestFont(t, dir, "mark.ttf", "MarkFam")
	h := &editHandler{configsPath: t.TempDir(), outDir: t.TempDir(), fontsDir: dir}
	idx := h.fontIndex(dir)
	if idx["MarkFam"] == "" {
		t.Fatalf("fontIndex missing MarkFam: %v", idx)
	}
	// A font-family used by the SVG (e.g. a [font:] run or template text)
	// resolves through the index into an @font-face rule.
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><tspan font-family="MarkFam">x</tspan></svg>`
	out := h.injectFontFaces(svg, Config{})
	if !strings.Contains(out, `font-family:'MarkFam'`) || !strings.Contains(out, `/asset/fonts/mark.ttf`) {
		t.Fatalf("index font not injected: %s", out)
	}
	// Comma fallback lists try each candidate.
	svg = `<svg xmlns="http://www.w3.org/2000/svg"><tspan font-family="Nope, MarkFam, sans">x</tspan></svg>`
	if out := h.injectFontFaces(svg, Config{}); !strings.Contains(out, `'MarkFam'`) {
		t.Fatalf("fallback family not resolved: %s", out)
	}
}

func TestServeEditEndToEnd(t *testing.T) {
	root := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serveEdit(root, root, addr) }()

	// Poll ping until the server is up.
	var ping []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/api/ping") // #nosec G107 -- loopback test
		if err == nil {
			ping, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(ping), `"app":"logo-generator"`) {
		t.Fatalf("server never came up; ping = %s", ping)
	}
	resp, err := http.Post("http://"+addr+"/api/shutdown", "", nil) // #nosec G107 -- loopback test
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveEdit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not return")
	}
}

func TestEditShutdownEndpoint(t *testing.T) {
	called := make(chan struct{}, 1)
	h := newEditTestHandler(t)
	mux := editMux(h, func() { called <- struct{}{} })
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/shutdown", nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("shutdown callback not invoked")
	}
}

func TestEditMethodNotAllowed(t *testing.T) {
	h := newEditTestHandler(t)
	if w := doEdit(h, "POST", "/api/sites", "{}"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", w.Code)
	}
}

func TestEditSitesMissingDir(t *testing.T) {
	h := &editHandler{configsPath: filepath.Join(t.TempDir(), "missing")}
	if w := doEdit(h, "GET", "/api/sites", ""); w.Code == 200 {
		t.Fatal("missing configs dir should fail")
	}
}

func TestEditSiteErrors(t *testing.T) {
	h := newEditTestHandler(t)
	for _, target := range []string{"/api/site", "/api/site?name=../x", "/api/site?name=ghost"} {
		if w := doEdit(h, "GET", target, ""); w.Code == 200 {
			t.Fatalf("%s: expected error status", target)
		}
	}
}

func TestEditBadBodies(t *testing.T) {
	h := newEditTestHandler(t)
	for _, target := range []string{"/api/preview", "/api/render", "/api/template-analyze", "/api/template-fill"} {
		if w := doEdit(h, "POST", target, "{oops"); w.Code == 200 {
			t.Fatalf("%s: bad JSON should fail", target)
		}
	}
}

func TestEditSaveErrors(t *testing.T) {
	h := newEditTestHandler(t)
	if w := doEdit(h, "POST", "/api/save", "{}"); w.Code == 200 {
		t.Fatal("missing name should fail")
	}
	if w := doEdit(h, "POST", "/api/save?name=acme", "{oops"); w.Code == 200 {
		t.Fatal("bad JSON should fail")
	}
}

func TestEditRenderAllMissingTools(t *testing.T) {
	h := newEditTestHandler(t)
	t.Setenv("PATH", t.TempDir()) // no resvg/cwebp/avifenc on PATH
	cfg := `{"name":"acme","template":"templates/logo.svg","output":{"formats":["svg","png"]}}`
	if w := doEdit(h, "POST", "/api/render", cfg); w.Code == 200 {
		t.Fatal("missing tools should fail at preflight")
	}
}

func TestAssetFileGuardsAndCache(t *testing.T) {
	h := newEditTestHandler(t)
	mustMkdir(t, filepath.Join("assets", "images"))
	writeFile(t, filepath.Join("assets", "images", "mark.png"), "x")
	w := doEdit(h, "GET", "/asset/images/mark.png", "")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=300") {
		t.Fatalf("Cache-Control = %q", got)
	}
	for _, target := range []string{"/asset/nope/x.png", "/asset/images/../../etc/passwd"} {
		if w := doEdit(h, "GET", target, ""); w.Code == 200 {
			t.Fatalf("%s should not be served", target)
		}
	}
}

func TestNameString(t *testing.T) {
	cases := []struct {
		platform []byte
		raw      []byte
		want     string
	}{
		{[]byte{0, 3}, []byte{0, 'A'}, "A"},  // Windows UTF-16BE
		{[]byte{0, 0}, []byte{0, 'B'}, "B"},  // Unicode UTF-16BE
		{[]byte{0, 1}, []byte("mac"), "mac"}, // Macintosh ASCII
		{[]byte{0, 2}, []byte("x"), ""},      // unknown platform
	}
	for _, c := range cases {
		if got := nameString(c.platform, c.raw); got != c.want {
			t.Errorf("nameString(%v) = %q, want %q", c.platform, got, c.want)
		}
	}
}

func TestCSSSafe(t *testing.T) {
	if got := cssSafe(`a'b;c{d}\e`); got != "abcde" {
		t.Fatalf("cssSafe = %q", got)
	}
}

func TestFontFamilyNameNonFont(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "n.txt")
	writeFile(t, bad, "not a font")
	if got := fontFamilyName(bad); got != "" {
		t.Fatalf("non-font family = %q", got)
	}
	if got := fontFamilyName(filepath.Join(t.TempDir(), "missing.ttf")); got != "" {
		t.Fatal("missing file should give empty family")
	}
}

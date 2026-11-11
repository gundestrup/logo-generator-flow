package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configs, "acme.json"), []byte(editTestConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "logo.svg"), []byte(editTestTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return &editHandler{configsPath: configs, outDir: filepath.Join(root, "output")}
}

func doEdit(h *editHandler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	w := httptest.NewRecorder()
	switch {
	case target == "/api/sites":
		h.sites(w, req)
	case strings.HasPrefix(target, "/api/site"):
		h.site(w, req)
	case target == "/api/preview":
		h.preview(w, req)
	case target == "/api/render":
		h.renderAll(w, req)
	case strings.HasPrefix(target, "/api/save"):
		h.save(w, req)
	case strings.HasPrefix(target, "/out/"):
		h.outputFile(w, req)
	}
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
	if err := os.MkdirAll(filepath.Join(h.outDir, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
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

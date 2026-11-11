package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupSites creates a temp project: a template plus n site configs
// (site-a, site-b, ...) rendering svg only.
func setupSites(t *testing.T, n int) (root, configs, out string) {
	t.Helper()
	root = t.TempDir()
	configs = filepath.Join(root, "configs")
	out = filepath.Join(root, "out")
	// #nosec G301 -- test fixture in a private t.TempDir().
	if err := os.Mkdir(configs, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "t.svg"), `<svg><text>{{text1}}</text></svg>`)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("site-%c", 'a'+i)
		cfg := fmt.Sprintf(`{
			"name": %q, "template": "t.svg",
			"output": {"formats": ["svg"]},
			"text": {"text1": %q}
		}`, name, "hello-"+name)
		writeFile(t, filepath.Join(configs, name+".json"), cfg)
	}
	t.Chdir(root)
	return root, configs, out
}

func TestRunAllRendersEverySite(t *testing.T) {
	_, configs, out := setupSites(t, 2)
	if err := runAll(configs, out, ""); err != nil {
		t.Fatal(err)
	}
	for _, site := range []string{"site-a", "site-b"} {
		// #nosec G304 -- reading our own rendered output in a test.
		b, err := os.ReadFile(filepath.Join(out, site, "logo.svg"))
		if err != nil {
			t.Fatalf("missing %s: %v", site, err)
		}
		if !strings.Contains(string(b), "hello-"+site) {
			t.Fatalf("%s not rendered: %s", site, b)
		}
	}
}

func TestRunAllSiteFilter(t *testing.T) {
	_, configs, out := setupSites(t, 2)
	if err := runAll(configs, out, "site-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "site-a")); !os.IsNotExist(err) {
		t.Fatal("site-a should be filtered out")
	}
	if _, err := os.Stat(filepath.Join(out, "site-b", "logo.svg")); err != nil {
		t.Fatal("site-b missing")
	}
}

func TestRunAllFilterNoMatch(t *testing.T) {
	_, configs, out := setupSites(t, 1)
	err := runAll(configs, out, "nope")
	if err == nil || !strings.Contains(err.Error(), "no sites matched") {
		t.Fatalf("want no-match error, got %v", err)
	}
}

func TestRunAllKeepsGoingOnBadConfig(t *testing.T) {
	_, configs, out := setupSites(t, 1)
	writeFile(t, filepath.Join(configs, "broken.json"), `{invalid`)

	err := runAll(configs, out, "")
	if err == nil || !strings.Contains(err.Error(), "broken.json") || !strings.Contains(err.Error(), "1 site(s) failed") {
		t.Fatalf("want failure summary naming broken.json, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "site-a", "logo.svg")); err != nil {
		t.Fatal("good site should still render")
	}
}

func TestRunAllDuplicateNames(t *testing.T) {
	_, configs, out := setupSites(t, 1)
	writeFile(t, filepath.Join(configs, "copy.json"), `{
		"name": "site-a", "template": "t.svg",
		"output": {"formats": ["svg"]}, "text": {"text1": "dup"}
	}`)
	err := runAll(configs, out, "")
	if err == nil || !strings.Contains(err.Error(), `duplicate site name "site-a"`) {
		t.Fatalf("want duplicate name error, got %v", err)
	}
}

func TestRunAllNothingToRender(t *testing.T) {
	dir := t.TempDir()
	err := runAll(dir, filepath.Join(dir, "out"), "")
	if err == nil || !strings.Contains(err.Error(), "no *.json") {
		t.Fatalf("want no-configs error, got %v", err)
	}
}

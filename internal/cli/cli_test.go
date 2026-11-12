package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/logo-generator/internal/testutil"
)

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"-version"}, &out, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "logo-generator") {
		t.Fatalf("version output = %q", out.String())
	}
}

func TestRunBadFlag(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"-nope"}, &out, "test"); err == nil {
		t.Fatal("expected flag parse error")
	}
}

func TestRunMissingConfigs(t *testing.T) {
	var out bytes.Buffer
	missing := filepath.Join(t.TempDir(), "nope")
	if err := Run([]string{"-configs", missing}, &out, "test"); err == nil {
		t.Fatal("expected error for missing configs dir")
	}
}

// mkConfigsDir creates an empty configs directory inside a fresh temp root.
func mkConfigsDir(t *testing.T) string {
	t.Helper()
	configs := filepath.Join(t.TempDir(), "configs")
	testutil.MustMkdir(t, configs)
	return configs
}

// writeSite writes a minimal svg-only site config (and its template) into
// root/configs so renderSite needs no external tools.
func writeSite(t *testing.T, root, name string) {
	t.Helper()
	tpl := filepath.Join(root, name+".svg")
	testutil.WriteFile(t, tpl, `<svg><text>{{text1}}</text></svg>`)
	testutil.WriteFile(t, filepath.Join(root, "configs", name+".json"), `{
		"name": "`+name+`",
		"template": "`+tpl+`",
		"text": {"text1": "hi"},
		"output": {"formats": ["svg"]}
	}`)
}

func TestRunRenderSVGOnly(t *testing.T) {
	configs := mkConfigsDir(t)
	root := filepath.Dir(configs)
	writeSite(t, root, "acme")
	out := filepath.Join(root, "out")
	var log bytes.Buffer
	if err := Run([]string{"-configs", configs, "-out", out}, &log, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "acme", "logo.svg")); err != nil {
		t.Fatalf("logo.svg not written: %v", err)
	}
}

func TestRunSitesFilter(t *testing.T) {
	configs := mkConfigsDir(t)
	root := filepath.Dir(configs)
	writeSite(t, root, "acme")
	writeSite(t, root, "other")
	out := filepath.Join(root, "out")
	var log bytes.Buffer
	if err := Run([]string{"-configs", configs, "-out", out, "-sites", "acme"}, &log, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "acme", "logo.svg")); err != nil {
		t.Fatal("acme should render")
	}
	if _, err := os.Stat(filepath.Join(out, "other")); !os.IsNotExist(err) {
		t.Fatal("other should be filtered out")
	}
	// Unknown filter → "no sites matched".
	if err := Run([]string{"-configs", configs, "-out", out, "-sites", "ghost"}, &log, "test"); err == nil {
		t.Fatal("expected no-sites-matched error")
	}
}

func TestRunSiteFailures(t *testing.T) {
	configs := mkConfigsDir(t)
	root := filepath.Dir(configs)
	writeSite(t, root, "acme")
	out := filepath.Join(root, "out")
	var log bytes.Buffer

	// Duplicate site name → collected failure.
	testutil.WriteFile(t, filepath.Join(configs, "dup.json"),
		`{"name":"acme","template":"x.svg","output":{"formats":["svg"]}}`)
	if err := Run([]string{"-configs", configs, "-out", out}, &log, "test"); err == nil {
		t.Fatal("duplicate site name should fail the run")
	}
	if err := os.Remove(filepath.Join(configs, "dup.json")); err != nil {
		t.Fatal(err)
	}

	// Broken config → loadConfig failure.
	testutil.WriteFile(t, filepath.Join(configs, "bad.json"), "{oops")
	if err := Run([]string{"-configs", configs, "-out", out}, &log, "test"); err == nil {
		t.Fatal("invalid config should fail the run")
	}
	if err := os.Remove(filepath.Join(configs, "bad.json")); err != nil {
		t.Fatal(err)
	}

	// Missing template → renderSite failure.
	testutil.WriteFile(t, filepath.Join(configs, "dead.json"),
		`{"name":"dead","template":"no.svg","output":{"formats":["svg"]}}`)
	if err := Run([]string{"-configs", configs, "-out", out}, &log, "test"); err == nil {
		t.Fatal("missing template should fail the run")
	}
}

func TestRunEmptyConfigs(t *testing.T) {
	configs := mkConfigsDir(t)
	var log bytes.Buffer
	if err := Run([]string{"-configs", configs, "-out", t.TempDir()}, &log, "test"); err == nil {
		t.Fatal("empty configs dir should report nothing to render")
	}
}

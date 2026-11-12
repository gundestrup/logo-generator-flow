// Package testutil holds fixture helpers shared by tests across internal
// packages.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteFile writes a fixture file inside the test's temp tree.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	// #nosec G301 -- test fixture dir in a private t.TempDir().
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// MustMkdir creates a fixture dir inside the test's temp tree.
func MustMkdir(t *testing.T, path string) {
	t.Helper()
	// #nosec G301 -- test fixture dir in a private t.TempDir().
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

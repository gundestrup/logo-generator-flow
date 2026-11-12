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

// MakeFont builds a minimal sfnt binary (TTF) carrying one Windows name
// record so FamilyName-style parsers have a deterministic fixture.
func MakeFont(t *testing.T, dir, name, family string) string {
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
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(p, append(append(hdr, rec...), nt...), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

package fonts

import (
	"os"
	"path/filepath"
	"testing"

	"example.com/logo-generator/internal/testutil"
)

func TestFamilyName(t *testing.T) {
	dir := t.TempDir()
	if got := FamilyName(testutil.MakeFont(t, dir, "x.ttf", "TestFam")); got != "TestFam" {
		t.Fatalf("got %q", got)
	}
	bad := filepath.Join(dir, "bad.ttf")
	// #nosec G306 -- test fixture in a private t.TempDir().
	if err := os.WriteFile(bad, []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := FamilyName(bad); got != "" {
		t.Fatalf("garbage font should give empty family, got %q", got)
	}
	if got := FamilyName(filepath.Join(dir, "missing.ttf")); got != "" {
		t.Fatal("missing file should give empty family")
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
		if got := NameString(c.platform, c.raw); got != c.want {
			t.Errorf("NameString(%v) = %q, want %q", c.platform, got, c.want)
		}
	}
}

func TestIndex(t *testing.T) {
	dir := t.TempDir()
	testutil.MakeFont(t, dir, "x.ttf", "IdxFam")
	testutil.WriteFile(t, filepath.Join(dir, "n.txt"), "not a font")
	testutil.WriteFile(t, filepath.Join(dir, ".hidden.ttf"), "x")
	idx := Index(dir)
	if idx["IdxFam"] != filepath.ToSlash(filepath.Join(dir, "x.ttf")) {
		t.Fatalf("Index = %v", idx)
	}
	if len(idx) != 1 {
		t.Fatalf("Index should skip non-fonts, got %v", idx)
	}
}

// Package fonts reads family names out of sfnt font files (TTF/OTF/TTC)
// so the same files can be addressed by the font-family values SVGs use.
package fonts

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Index maps font family → file path for every parseable font under dir.
func Index(dir string) map[string]string {
	idx := map[string]string{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if fam := FamilyName(p); fam != "" {
				idx[fam] = filepath.ToSlash(p)
			}
		}
	}
	return idx
}

// FamilyName reads the family name out of a TTF/OTF/TTC sfnt name
// table (nameID 16 typographic family, else nameID 1). Returns "" for
// formats it can't parse (WOFF/WOFF2) so callers can fall back.
func FamilyName(path string) string {
	b := readFont(path)
	if b == nil {
		return ""
	}
	if toff, ok := nameTableOff(b); ok {
		return familyFromRecords(b, toff)
	}
	return ""
}

// readFont loads up to 8 MiB of a font file; nil on any failure.
func readFont(path string) []byte {
	f, err := os.Open(path) // #nosec G304 -- font files under assets/fonts or from config
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 8<<20))
	if err != nil || len(b) < 16 {
		return nil
	}
	return b
}

// nameTableOff locates the sfnt "name" table (following the first TTC
// member's directory when the file is a collection).
func nameTableOff(b []byte) (int, bool) {
	off := 0
	if string(b[:4]) == "ttcf" {
		off = int(binary.BigEndian.Uint32(b[12:16]))
	}
	if off+6 > len(b) {
		return 0, false
	}
	numTables := int(binary.BigEndian.Uint16(b[off+4:]))
	for i := 0; i < numTables; i++ {
		rec := off + 12 + 16*i
		if rec+16 > len(b) {
			break
		}
		if string(b[rec:rec+4]) == "name" {
			toff := int(binary.BigEndian.Uint32(b[rec+8:]))
			return toff, toff+6 <= len(b)
		}
	}
	return 0, false
}

// familyFromRecords scans name-table records for the typographic family
// (nameID 16), falling back to the plain family (nameID 1).
func familyFromRecords(b []byte, toff int) string {
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
		if s := NameString(b[nr:nr+4], b[noff:noff+nlen]); s != "" {
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

// NameString decodes a name-table record per its platform: Windows (3) and
// Unicode (0) store UTF-16BE; Macintosh (1) is ASCII-compatible.
func NameString(platform []byte, raw []byte) string {
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

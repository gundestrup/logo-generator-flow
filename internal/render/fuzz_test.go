package render

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"
)

// FuzzStyledText asserts the markup parser always emits well-formed XML:
// balanced tspans, escaped text, no raw control bytes that would corrupt
// the SVG document.
func FuzzStyledText(f *testing.F) {
	for _, s := range []string{
		"plain", "**bold**", "*it*", "__under__", "[#ff0000]red[/]",
		"[color:#aabbcc]c[/]", "[font:Mono]x[/]", "[size:2em]x[/]",
		"**a *b* c**", `\*esc\* \[lit\]`, "[/]", "**unclosed", "a[/]b",
		"<tag>&\"'", "[font:]empty[/]", "\x00\x01ctrl", "héllo 😀",
		"[font:<script>]inj[/]", "[[[[", "]]]]",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := styledText(s)
		dec := xml.NewDecoder(strings.NewReader("<r>" + out + "</r>"))
		for {
			_, err := dec.Token()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				t.Fatalf("malformed XML for %q: %v\nout: %s", s, err, out)
			}
		}
	})
}

// FuzzRecolorSVG asserts recoloring never drops fill="none" markers and
// never panics on arbitrary SVG bytes.
func FuzzRecolorSVG(f *testing.F) {
	for _, s := range []string{
		`<svg><rect fill="#123456"/></svg>`,
		`<svg><rect fill="none" stroke="red"/></svg>`,
		`<svg><path d="M0 0"/></svg>`,
		"", "not xml", `<svg fill="#fff" fill="#000"/>`,
	} {
		f.Add(s, "#ff0000", "#00ff00")
	}
	f.Fuzz(func(t *testing.T, svg, fill, stroke string) {
		out := string(recolorSVG([]byte(svg), fill, stroke))
		if got, want := strings.Count(out, `fill="none"`), strings.Count(svg, `fill="none"`); got != want {
			t.Fatalf("fill=none count changed %d → %d", want, got)
		}
	})
}

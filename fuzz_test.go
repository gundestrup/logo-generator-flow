package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
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

// FuzzParseColor asserts acceptance exactly matches the #RRGGBB grammar
// and that decoded channel values correspond to the input digits.
func FuzzParseColor(f *testing.F) {
	for _, s := range []string{
		"#ffffff", "000000", "#ABCDEF", "#fff", "red", "", "#1234567",
		"##123456", "#12 456", "#00000g", "0x123456",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, err := parseColor(s)
		if hexColor.MatchString(s) != (err == nil) {
			t.Fatalf("parseColor(%q) err=%v but regex match=%v", s, err, err == nil)
		}
		if err != nil {
			return
		}
		n, _ := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
		// #nosec G115 -- n ≤ 0xffffff guaranteed by the #RRGGBB grammar.
		if c.R != uint8(n>>16) || c.G != uint8(n>>8&0xff) || c.B != uint8(n&0xff) || c.A != 255 {
			t.Fatalf("parseColor(%q) = %+v, want decode of %x", s, c, n)
		}
	})
}

// FuzzImageSlotUnmarshal asserts a canonical-form round trip: whatever the
// decoder accepts re-encodes and re-decodes to an identical slot.
func FuzzImageSlotUnmarshal(f *testing.F) {
	for _, s := range []string{
		`"a.svg"`, `{"src":"a.svg","fill":"#fff","stroke":"#000"}`,
		`{"src":"a.svg"}`, `123`, `null`, `{"nope":1}`, `[1]`, `true`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b string) {
		var s ImageSlot
		if err := json.Unmarshal([]byte(b), &s); err != nil {
			return
		}
		out, err := json.Marshal(map[string]string{"src": s.Src, "fill": s.Fill, "stroke": s.Stroke})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var s2 ImageSlot
		if err := json.Unmarshal(out, &s2); err != nil || s2 != s {
			t.Fatalf("round trip %q → %s → %+v (err %v), started as %+v", b, out, s2, err, s)
		}
	})
}

// FuzzTextSlotUnmarshal — same canonical round-trip for text slots.
func FuzzTextSlotUnmarshal(f *testing.F) {
	for _, s := range []string{
		`"hi"`, `{"text":"hi","color":"#000"}`, `{"text":"hi"}`,
		`123`, `null`, `{"nope":1}`, `"unclosed`, `{"text":5}`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b string) {
		var s TextSlot
		if err := json.Unmarshal([]byte(b), &s); err != nil {
			return
		}
		out, err := json.Marshal(map[string]string{"text": s.Value, "color": s.Color})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var s2 TextSlot
		if err := json.Unmarshal(out, &s2); err != nil || s2 != s {
			t.Fatalf("round trip %q → %s → %+v (err %v), started as %+v", b, out, s2, err, s)
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

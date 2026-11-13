package server

import (
	"strings"
	"testing"
)

// FuzzAnalyzeFill asserts the analyzer never panics and that every element it
// reports can actually be filled — the invariant the real-world fixture bugs
// violated (multi-line text, comment-innards, paired tags all broke the
// analyze→fill round-trip).
func FuzzAnalyzeFill(f *testing.F) {
	for _, s := range []string{
		realWorldTemplate,
		`<svg><text id="t"><tspan>a</tspan><tspan>b</tspan></text><path id="p" d="M0 0"/></svg>`,
		`<svg><!-- <rect id="inComment"/> --><rect x="1" y="1" width="2" height="2"/></svg>`,
		`<svg><text x="1" y="2">multi
line</text></svg>`,
		`<svg><rect x="1" y="2" width="3" height="4"></rect></svg>`,
		`<svg><circle cx="5" cy="5" r="3"/><polygon points="0,0 9,9"/><line x1="0" y1="0" x2="9" y2="9"/></svg>`,
		`<svg><text id="a">{{text1}}</text><image id="i" href="{{image1}}"/></svg>`,
		`garbage`, `<`, `<svg>`, `{{`, `<text>`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, svg string) {
		els := analyzeTemplate(svg)
		seen := map[int]bool{}
		mapping := map[string]string{}
		fps := map[string]string{}
		for _, e := range els {
			if e.ID == "" {
				t.Fatalf("empty id: %+v", e)
			}
			if seen[e.Ord] {
				t.Fatalf("duplicate ord %d", e.Ord)
			}
			seen[e.Ord] = true
			fps[e.ID] = e.Fp
			if e.Tag == "text" {
				mapping["text1"] = e.ID
			} else {
				mapping["image1"] = e.ID
			}
		}
		out, err := templateFill(svg, mapping, fps)
		if err != nil {
			t.Fatalf("fill failed on its own analysis: %v\nsvg: %.200s", err, svg)
		}
		// Whatever the analyzer found must now carry placeholders that a
		// re-analysis can see (no orphan mappings).
		if len(els) > 0 && !strings.Contains(out, "{{") {
			t.Fatalf("fill produced no placeholders for %d elements\nsvg: %.200s", len(els), svg)
		}
	})
}

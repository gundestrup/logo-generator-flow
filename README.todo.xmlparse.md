# README.todo — XML parsing for template analysis

## Background

Template analysis + fill (`internal/server/server.go`: `analyzeTemplate`,
`templateFill`, `fillTextInner`, `recolorSVG` in render) parse SVG with
regular expressions over the raw bytes. SVG is XML — regexes assume
formatting and keep leaking formatting-variance bugs:

- `(?s)` needed for multi-line `<text>` (missed all multi-line elements)
- `cx/cy/r` vs `x/y/w/h` per-shape attribute vocabulary
- `stroke-width=` aliasing `width=`; `data-id=` aliasing `id=`
- markup inside `<!-- -->`, `<style>`, `<defs>` scanned as real elements
- self-closing `<rect/>` vs paired `<rect></rect>` splice handling

Current mitigation (committed): `tplSkipRe` blanks comments/style/defs,
whitespace-prefixed attr names, `(?s)` matchers, corpus test
`realWorldTemplate` + `FuzzAnalyzeFill` covering the analyze→fill
round-trip.

## Proposal

Port `analyzeTemplate`/`templateFill` to `encoding/xml` token stream
(`xml.Decoder` with `.RawToken` to keep positions), recording element
byte ranges for splicing. `encoding/xml` lacks position info — options:

- `RawToken` + manual offset tracking via `Decoder.InputOffset()`
- a byte-scan pass that maps token order to offsets

Benefits: attribute parsing is exact (no aliasing), comments/CDATA/
processing instructions are structurally invisible, entities decode for
free, and `<g>` groups could become mappable containers for free.

Caution: resvg tolerance — the editor also _renders_ these templates, so
well-formedness is already a soft requirement, but malformed templates
that regex "handled" would now fail — needs a graceful fallback or a
clear error message.

## Scope estimate

`analyzeTemplate`, `templateFill`, `fillTextInner` + tests — medium.
`recolorSVG` (render path) is a separate, lower-risk candidate since it
rewrites attribute _values_ not structure.

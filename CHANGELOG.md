# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.1] - 2026-11-14

### Changed

- Escape now also closes the map-template modal (modal Escape handling is
  centralized in `init()` instead of per-overlay `x-on:keydown` attributes).

## [0.5.0] - 2026-11-13

### Added

- Template mapping now works on templates **without element ids**:
  `analyzeTemplate` numbers every mappable element `auto-<ord>` by
  document order and `templateFill` injects a real `id="auto-N"` plus
  the `{{slot}}` placeholder — the filled file is stable afterwards.
  `polygon` and `line` are mappable too, and text elements carry a
  content snippet so map labels read `"jekyll-client-…"` not just
  `auto-8`.
- `scripts/smoke-editor.mjs` + `make smoke` — headless-Chrome CDP smoke
  of the editor UI (site load, font/image modals, template select,
  format toggles + dirty-save, map modal) asserting zero console errors;
  runs as a `smoke` job in CI. Closes the frontend test gap — `app.js`
  previously had zero automated coverage.
- Golden SVG test (`internal/render/golden_test.go`, `-update` to
  regen) — byte-exact output lock covering font-family quoting, styled
  markup, colored slots, svg recolor, and base64 embedding.
- `make cover-check` (in `make check`) — per-package coverage floor,
  `MIN=80` default.
- `FuzzAnalyzeFill` fuzzes the analyze→fill round-trip: every element
  the analyzer reports must be fillable, with no orphan mappings.
- Remap detection: each element gets a fingerprint (tag + normalized
  open tag + text) sent back in `template-fill`'s new `fps` field — if
  the template changed since the map modal scanned it, fill fails
  loudly with "changed since mapping — reopen map template to remap"
  instead of writing placeholders into the wrong element.

### Fixed

- Elements inside `<!-- comments -->`, `<style>`, and `<defs>` were
  scanned as mappable regions — they are blanked (length-preserving, so
  splice offsets stay valid) before analysis. `data-id="…"` no longer
  aliases the real `id` attribute.
- Paired (non-self-closing) elements like `<rect …></rect>` could not be
  filled as image targets — fill now locates every element by document
  offset and swallows the adjacent close tag; fingerprints are verified
  for real ids too, not only `auto-*`.
- Multi-line `<text>` elements were invisible to template analysis —
  the inner-content match couldn't span newlines (`(?s)` now set on the
  element and tspan matchers).
- `circle`/`ellipse` elements produced 0×0 (or stroke-width-polluted)
  bounding boxes — `cx`/`cy`/`r`/`rx`/`ry` were never captured and
  `stroke-width=` aliased `width=`.
- A `<text>` mapped to a slot but containing no `<tspan>`s lost the
  placeholder entirely — it now receives `{{slot}}` as plain content.

### Added

- Global settings card: `name` input, `template` picker (new
  `GET /api/templates`), and output-format checkboxes (svg is always
  written — shown checked+disabled; png/webp/avif/jpeg toggle
  `output.formats`) — all edit the JSON config without touching the
  textarea. A `save` button appears in the card header while the
  config has unsaved changes. Template changes re-analyze the new
  template (regions + viewBox) via a new `svg` field on
  `POST /api/template-analyze`.

### Changed

- Global font row: the dropdown is replaced by an inline preview
  (`Family  AaBbGg 123`) with the `Aa…` picker button at the right;
  shows a dimmed `none selected` placeholder when no font is set.
- Font and image modals close on backdrop click and Escape (row clicks
  already closed the font modal).

### Fixed

- Global font control was always empty — `loadFonts()` captured
  `fontSel` before its `await` and restored the stale `""` over the
  value `loadSite` had just read from `render.fontFiles` (async race).

## [0.4.0] - 2026-11-13

### Added

- Font picker modal — an `Aa…` button next to each font select opens a
  list where every family renders sample text in its own glyphs (the
  markup picker previews your actual textarea selection). Global mode
  picks a font file, inline mode wraps the selection in `[font:…]`.

### Fixed

- `fonts.css` loaded render-blocking; now deferred (`media="print"` →
  `all` on load) — pickers don't need it before first paint.
- `/asset/` responses cached only 300s; bumped to `max-age=3600` for
  font/image content files.
- Font-modal rows rendered ~180 DOM nodes at startup while hidden;
  the row getter now returns `[]` until the modal opens (page chrome
  DOM down ~60% on font-heavy setups).

## [0.3.0] - 2026-11-13

### Added

- Duplicate, rename, and delete configs from the editor — ⧉ / ✎ / ✕
  buttons next to the site select. `POST /api/duplicate?name=…&to=…`
  copies (rewriting the `name` field, refusing to overwrite), `POST
/api/rename` moves the file, `POST /api/delete` removes it (refuses
  the last remaining config; the UI asks for confirmation first).
- `/api/sites` now returns `{name, file}` pairs — the site dropdown
  shows each config's own `name` field while endpoints keep addressing
  files by basename.
- `?site=<name>` deep-link in the editor — loads a specific site on
  open instead of the alphabetically first one.
- Font pickers render each option in its own glyphs plus a live
  `AaBbGg 123` preview next to the select (`GET /fonts.css` serves the
  faces; `internal/fonts` shares the sfnt family-name parser).
- Exported `logo.svg` embeds `@font-face` rules (base64 data URIs) for
  the font families it references — self-contained output that renders
  correctly in browsers without local font installs. Applied after
  rasterization so resvg sees a clean document.
- Image editor: `recolor` toggle — unchecked keeps the image's original
  colors (no `fill`/`stroke` keys emitted) instead of forcing defaults.
- Render now prints a `note:` for config slots (text/images) that have
  no `{{…}}` placeholder in the template — previously dropped silently.
- `make bump V=x.y.z` (`scripts/bump-version.sh`) — one-command release
  bump: writes VERSION and copies it into the places that must hold it
  statically (package.json field, README shields badge, new dated
  CHANGELOG section), then runs `version-check`. Refuses if the version
  already has a CHANGELOG section.
- `scripts/capture-editor.mjs` — CDP capture of the live `-ui` editor:
  picks the first site with region overlays, clicks a text region, and
  screenshots it. `docs/assets/demo.html` now embeds this real capture
  instead of a hand-drawn browser mock, so the demo can't drift from
  the actual UI.
- `docs/assets/cli.html` + `logo-generator-cli.*` media — a second
  animation showing a full `./logo-generator` render and the `-ui`
  launch, typed out progressively (10 frames @ 1.2fps).

### Fixed

- Multi-word font families containing digits (e.g. `[font:Source Sans 3]`)
  rendered as the fallback font — resvg rejected the unquoted CSS value.
  Family names are now emitted as quoted CSS values; `--font-family`
  keeps the raw name.
- Slot editor size input was nearly invisible (dark-on-dark, unlabelled)
  and cleared itself after applying — now a visibly bordered control
  that keeps the last value.
- `x-bind:style` rewrote the whole `style` attribute and clobbered
  `x-show`'s `display:none` — previews use `x-bind:hidden` instead.
- Editor startup renders both slot-editor variants to measure height;
  the measured value is now cached in `localStorage` and applied
  synchronously on repeat visits (re-measured in the background).

## [0.2.0] - 2026-11-12

### Added

- Slot editor is a permanent card pinned below the template card (3px
  gap): fixed height measured at startup as the taller of the text and
  image variants, a placeholder hint when nothing is selected, and the
  card never resizes the column when switching editor kinds.
- Font-size control in the slot editor's inline-markup toolbar: a small
  number input wraps the textarea selection in `[size:N]…[/]` (the
  mini-markup tag the renderer already supports).
- Browser editor (`-edit` / `make edit`): loopback UI on
  `http://127.0.0.1:8181/` (flag: `-ui`, alias `-edit`) with a live JSON
  editor, template preview, debounced SVG render, "render all formats"
  output links, and save-back to `configs/<name>.json`. Toolbar pickers
  insert `assets/images/*` into `image1-4` (with an svg fill-recolor
  option, separate fill + stroke pickers), pick `assets/fonts/*` into `render.fontFiles`/`fontFamily`,
  and set per-slot text colors.
- Font picker works end-to-end: `/api/fonts` reports each font's real
  family name (parsed from the sfnt name table), `render.fontFamily`
  overrides the template's `font-family` declarations — presentation
  attributes in either quote style and CSS `font-family:` rules in
  `<style>` blocks, including entity-escaped quotes (`&apos;`) —
  (`[font:…]` markup still wins locally), and the preview injects
  `@font-face` for every family the SVG references so custom fonts
  render in the browser too.
- Contextual slot editor: clicking a template region opens an `edit`
  panel with only the relevant controls — text slots get a markup
  toolbar (B/I/U/inline color/per-run font), content field, and slot
  color; image slots get `pick…`, svg fill + stroke, and a reset.
  Changes preview live through a draft state; **apply** (shown only when
  the draft differs) merges into the JSON, and **save** persists.
- Preview backdrop selector in the generated headline
  (checker/white/black/dark/custom) — CSS-only, never touches the
  rendered output.
- `GET /api/ping` + `POST /api/shutdown`: `-ui` self-heals `EADDRINUSE`
  by gracefully stopping a previous logo-generator instance on the port.
- `scripts/check-dom-refs.mjs`: cross-references every literal
  `$("id")`/`getElementById`/`querySelector("#id")` in the HTML files
  against defined ids — runs in `npm run check`.
- Fuzz targets for `styledText`, `parseColor`, slot unmarshalling, and
  `recolorSVG`; opt-in `//go:build integration` test renders through
  real resvg/cwebp/avifenc.
- Template mapping flow: `POST /api/template-analyze` lists `id`'d
  elements with bounding boxes; `POST /api/template-fill` rewrites a
  template into `{{slot}}` form — figure boxes become `<image>` elements
  at the box geometry (`preserveAspectRatio="xMidYMid meet"`), text
  elements distribute assigned slots across their tspans. Editor gains a
  "map template…" regions card; slots extended to `text1-6`/`image1-6`.
- Inline text markup — `**bold**`, `*italic*`, `__underline__`,
  `[#hex]…[/]`, `[font:…]…[/]`, `[size:…]…[/]` — renders as styled
  `<tspan>` runs; a mini toolbar wraps the field selection.
- Slot object forms: `textN` may be `{"text": "...", "color": "#rrggbb"}`
  (renders via `<tspan fill>`), and `imageN` may be
  `{"src": "...", "fill": "#hex", "stroke": "#hex"}` — fill/stroke
  recolor `.svg` sources only, rewriting attributes and keeping
  `fill="none"`.
- `README.todo.vectorcraft`: full analysis of the `.vectorcraft` format
  and an implementation plan for accepting it as a template source
  (vectorcraft-cli export → existing `templateFill` slot injection);
  `README.todo.*` convention documented in `AGENTS.md` alongside a new
  working-agreement section (docs-with-changes, tests for new code,
  changelog entries) and MCP server guidance (`knowledge-base`, `wvf` —
  incl. `wvf scan` on local demo/editor pages for HTML analysis and bug
  finding).

### Changed

- Source layout is `cmd/logo-generator` + `internal/{cli,config,render,server,testutil}`
  (house convention): thin `main`, `cli.Run` orchestrates, the editor
  server and its embedded UI live in `internal/server`.
- Editor UI rewritten on vendored **Bulma** (`1.0.4`) + **alpine-csp**
  (`3.17.5`) under `internal/server/static/vendor/` — declarative
  `x-model`/`x-show`/`x-for` bindings with the imperative logic in an
  `Alpine.data` component (`static/app.js`). No CDN, no build step;
  `scripts/vendor-ui.sh` refreshes the vendored files from pinned
  `devDependencies`.
- `POST /api/render` streams Server-Sent Events when the client sends
  `Accept: text/event-stream` — one `{"file":…}` frame per written file
  plus a final `{"done":true,"files":[…]}`; plain JSON response is kept
  for other clients. `render.Site` gained an optional progress callback.
- Go 1.27.4 (fixes `GO-2026-6859`); CI image digest bumped
  (`golang:1.27.4-trixie`).
- Editor layout is three columns: global settings / edit / config ·
  template + regions card · generated. `map template…` + `outlines`
  moved into the template headline, backdrop into the generated
  headline; the config card is raw JSON only.
- `templates/` moved to `assets/templates/` — all user inputs (images,
  fonts, templates) now live under `assets/`; config `template` paths,
  the `template-fill` out guard, `.gitignore`, CI, and sonar exclusions
  updated.
- Editor tests drive the real route table via `server.Mux` + `ServeHTTP`
  (coverage ~83%); a11y labels added to unlabeled inputs.

### Fixed

- `template-fill` appended `-filled` to the config's current template
  each run, producing `…-filled-filled-filled.svg` chains; the suffix
  is now stripped before appending so fills are idempotent.
- Missing `mk-tools` markup crashed script evaluation on load
  (`Cannot set properties of null`) leaving the site dropdown empty.
- `styledText` passed XML-invalid control bytes through — `xmlSafe`
  strips runes XML 1.0 forbids.
- `fontFamily` had no effect on templates declaring fonts via `<style>`
  CSS rules (the common Illustrator/Inkscape export form).
- Editor CLS 0.177 → 0 (wvf/measured): the font `<select>` sized to its
  widest async-loaded option (~467 px) and reflowed all Bulma columns —
  `is-fullwidth` caps it at the label's width; header site select is
  fixed-width, `#status` truncates, `.preview` reserves
  `aspect-ratio`+`contain:size`, `scrollbar-gutter:stable` on `html`.
- `GET /favicon.ico` 404'd (wvf info); serves an inline SVG mark.
- `/static/*` responses carry `Cache-Control: no-cache` — the same URL
  serves new bytes after a rebuild, and `max-age` caching once stranded
  a stale `app.js` in the browser for an hour (a dead-UI bug). Injected
  `@font-face` rules use `font-display:swap`.
- Editor column layout: generated card sat 12px higher and inter-column
  gaps were uneven (Bulma column-padding model vs an unwrapped
  `card column`) — all three columns are now wrapped `.column`s with a
  uniform 12px `gap`, `padding:0` (max width) and `.preview` padding
  trimmed to 6px.
- Text regions rendered as ~1% dots — `analyzeTemplate` only extracted
  x/y for `<text>` (from `matrix()`), leaving w/h at 0. Position now
  also honors `translate()` and `x=`/`y=` attrs, and w/h are estimated
  from `font-size` × text length (with `text-anchor` and baseline
  adjustments), so text regions get real clickable boxes that highlight
  green on selection like image regions.
- Tall templates (e.g. 900×950) were clipped by the `.preview` box's
  hardcoded `aspect-ratio:1200/630` + `contain:size` — the stage had
  the right ratio but its parent didn't. Preview boxes now adopt the
  injected SVG's own viewBox/width-height ratio.
- `defer` on `app.js` broke Alpine init: deferred scripts execute in
  document order, so `alpine-csp.min.js` ran before the `alpine:init`
  listener registered — every expression failed `Undefined variable`,
  leaving the image-picker modal visible with dead buttons. `app.js`
  now loads first in `<head>` (both deferred), and the three `x-show`
  overlays carry `hidden` so a broken init degrades gracefully instead
  of blocking the page.

## [0.1.1] - 2026-11-11

### Fixed

- `go build` in CI job containers: `-buildvcs=false` (dubious-ownership VCS
  stamping failure).
- CodeFactor findings: split `renderSite`/`validateConfig` into helpers,
  merged duplicate `section` CSS selector in demo.html.
- SonarLint findings: `runAll` cognitive complexity, deduplicated `wrote`
  progress lines.

### Changed

- sonarqube-scan-action pinned to v8.4.1; Semgrep CI uses the registry
  `p/golang` + `p/security-audit` packs (Pro rules included, no token).
- sonar-project.properties configured for SonarCloud
  (org `gundestrup`, coverage import).

## [0.1.0] - 2026-11-10

### Added

- `VERSION` as the app-version source, injected via
  `-ldflags -X main.version`; new `-version` CLI flag. `version-check`
  (scripts/check-version.sh) enforces VERSION ↔ package.json ↔ CHANGELOG
  agreement and runs in `make check`.
- Git hooks (`hooks/pre-commit`, `hooks/pre-push`) + `make install-hooks`;
  `SKIP_HOOKS=1`/`SEMGREP_SKIP=1` escape hatches.
- `AGENTS.md` (house structure) and `.devin/config.json` permission policy;
  `.devin/config.local.json` stays gitignored.
- npm supply-chain hardening: `.npmrc` (`min-release-age=7`,
  `ignore-scripts=true`), Dependabot `cooldown: default-days: 7` plus an npm
  ecosystem entry, `npm audit --audit-level=high` and `npm ci
--ignore-scripts` in CI.
- govulncheck reachable-advisory gate (`make vuln-check`, pinned
  `govulncheck@v1.9.0`) in CI and `pre-push`.
- Frontend quality tooling matching Knowledge-BA46-Flow: `package.json`
  (Node >= 26.12.1), pinned `@biomejs/biome` 2.6.4 + `prettier` 3.9.10
  devDependencies, `biome.json`, `npm run check` / `make frontend-check`.
- `scripts/docs-demo.sh` + `scripts/screencast-convert.mjs`: headless-Chrome
  screenshots of the stepped `docs/assets/demo.html` mock, assembled into
  `docs/assets/demo/logo-generator-flow.{png,apng,webp,avif,svg}` — poster
  PNG, animated APNG/WebP/AVIF sequence, and a self-contained animated SVG
  (base64 WebP payload). Working frames live in `data/` (gitignored).

### Changed

- CI rewritten to the house pattern: all GitHub Actions pinned to commit
  SHAs with version comments, containers digest-pinned
  (`golang:1.27.3-trixie`, `node:26.12.1-bookworm`, `semgrep:1.181.0`),
  semgrep on the `p/golang` + `p/security-audit` rulesets, coverage via
  Codecov with opt-in SonarCloud (`SONAR_TOKEN` gate).
- gosec findings are annotated in place (`// #nosec — reason`) instead of
  blanket config exclusions; generated web assets stay 0644/0755.
- Makefile restructured to `check`/`ready` gate targets matching CI.
- Demo media now builds via the Node convert script instead of Makefile
  recipes; `make demo-media` delegates to `scripts/docs-demo.sh`.

### Added (initial release)

- CLI (`logo-generator`) that renders per-site SVG logos from JSON configs and
  a shared `{{slot}}` template, then exports via resvg → PNG, cwebp → WebP,
  avifenc → AVIF, and Go stdlib → JPEG.
- Config directory model: every `configs/*.json` is a site; `name` becomes the
  output subdirectory (`output/<name>/logo.*`). `-configs` accepts a directory
  or single file; `-sites` filters by name; `-out` sets the output root.
- Fixed slots: `text1`–`text4` and `image1`–`image4`. Empty values render
  nothing; unknown keys are config errors.
- Images embedded as base64 data URIs (local PNG/JPEG/SVG/WebP/AVIF or `data:`
  URIs); remote URLs rejected. Unresolved placeholders fail with a clear error.
- Per-site render options: width/height/dpi, per-format quality, WebP lossless,
  AVIF speed, JPEG background, and resvg font controls
  (`fontsDir`/`fontFiles`/`fontFamily`/`skipSystemFonts`); `assets/fonts/` is
  auto-detected.
- Tool preflight: missing resvg/cwebp/avifenc are reported together with an
  install hint; `logo.svg` is always written first; failing sites don't stop
  the rest.
- GUI demo (`demo/index.html`): three-pane view — config, template, generated
  output — served via `make demo`.
- Animated demo assets (`docs/assets/`): AVIF sequence, WebP, PNG fallback;
  rebuild with `make demo-media`.
- Quality tooling: golangci-lint config, test suite (race-enabled), semgrep
  rulesets, opt-in SonarCloud CI, GitHub Actions CI, dependabot, Makefile,
  `.editorconfig`/`.gitattributes`/`.gitignore`.

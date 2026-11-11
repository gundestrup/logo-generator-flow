# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Browser editor (`-edit` / `make edit`): loopback UI on
  `http://127.0.0.1:8181/` (flag: `-ui`, alias `-edit`) with a live JSON
  editor, template preview, debounced SVG render, "render all formats"
  output links, and save-back to `configs/<name>.json`. Toolbar pickers
  insert `assets/images/*` into `image1-4` (with an svg fill-recolor
  option, separate fill + stroke pickers), pick `assets/fonts/*` into `render.fontFiles`/`fontFamily`,
  and set per-slot text colors.
- Template mapping flow: `POST /api/template-analyze` lists `id`'d
  elements with bounding boxes; `POST /api/template-fill` rewrites a
  template into `{{slot}}` form — figure boxes become `<image>` elements
  at the box geometry (`preserveAspectRatio="xMidYMid meet"`), text
  elements distribute assigned slots across their tspans. Editor gains a
  "map template…" modal; slots extended to `text1-6`/`image1-6`;
  `templates/borg-collective.svg` + `configs/borg.json` added.
- Slot object forms: `textN` may be `{"text": "...", "color": "#rrggbb"}`
  (renders via `<tspan fill>`), and `imageN` may be
  `{"src": "...", "fill": "#hex", "stroke": "#hex"}` — fill/stroke
  recolor `.svg` sources only, rewriting attributes and keeping
  `fill="none"`.

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

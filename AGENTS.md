# AGENTS.md — logo-generator-flow

> **Single source of truth for all coding agents working on this project.**
> Tool-specific files (CLAUDE.md, .windsurfrules, .devin/global_rules.md)
> reference this document.

## Project overview

Go CLI that renders per-site logos: every `configs/*.json` is a site
definition (`name` → `output/<name>/logo.*`), merged into an SVG template via
fixed `{{text1-6}}` / `{{image1..6}}` slots. `resvg` produces the master PNG;
`cwebp`/`avifenc`/Go stdlib derive WebP/AVIF/JPEG from it, so all rasters are
pixel-identical. Images embed as base64 data URIs; remote URLs are rejected.

## Documentation index

| Topic                                | Document               |
| ------------------------------------ | ---------------------- |
| Usage, config schema, flags, fonts   | `README.md`            |
| Current and unreleased changes       | `CHANGELOG.md`         |
| Planned binary-release work          | `README.todo.build.md` |
| App version (SemVer, build-injected) | `VERSION`              |
| Agent permission policy              | `.devin/config.json`   |
| Future tasks / implementation plans  | `README.todo.<topic>`  |

## Commands / Development

```bash
make check           # version drift, fmt, vet, lint, race tests, build
make frontend-check  # biome lint + prettier check (npm run check)
make ready           # check + frontend + deps/semgrep/vuln scans (needs network)
make run             # build + render all configs/*.json
make demo            # render sites + serve GUI demo on :8080
make edit            # browser editor — live JSON editing on 127.0.0.1:8181
make demo-media      # rebuild docs/assets/demo media (Chrome + ffmpeg + img2webp)
make install-hooks   # point core.hooksPath at hooks/ (run once per clone)
```

Tests must use temp dirs and fake tool stubs — never require resvg/cwebp/
avifenc or network access. `go test -race ./...` is the gate.
`integration_test.go` (`//go:build integration`) exercises the real
resvg/cwebp/avifenc binaries — run `go test -tags=integration ./...`
when the tools are installed. `fuzz_test.go` fuzzes styledText,
parseColor, slot unmarshalers and recolorSVG.

## Quality gates

- `hooks/pre-commit`: staged-scope gofmt + `go vet`, prettier `--check`,
  semgrep (registry `p/golang` + `p/security-audit`).
- `hooks/pre-push`: `make check`, `frontend-check`, `semgrep`, `vuln-check`.
- `SKIP_HOOKS=1` and `SEMGREP_SKIP=1` are the documented escape hatches.
- CI (`.github/workflows/ci.yml`): all actions pinned to commit SHAs,
  containers digest-pinned (`golang:1.27.4-trixie`, `node:26.12.1`,
  `semgrep:1.181.0`). govulncheck is a hard gate. SonarCloud runs only when
  `SONAR_TOKEN` is set (Dependabot PRs need a Dependabot-scope secret).

## Conventions

- `VERSION` is the app version source; injected via
  `-ldflags -X main.version`. Keep it, `package.json.version`, and the latest
  dated `CHANGELOG.md` section in sync — `make version-check` enforces it.
- Layout is `cmd/logo-generator` (thin main) + `internal/{cli,config,render,server,testutil}`.
  The editor UI is `internal/server/static/` embedded via `//go:embed`;
  vendored bulma/alpine-csp live in `static/vendor/` — refresh with
  `scripts/vendor-ui.sh`, never CDN. alpine-csp is a restricted parser:
  no `x-html`, no object-literal/`||` expressions in attributes.
- `POST /api/render` streams SSE `data:` frames on
  `Accept: text/event-stream` (progress per written file); plain JSON
  for other clients — SSE is additive, not a replacement.
- `.npmrc` pins `min-release-age=7` + `ignore-scripts=true`; Dependabot
  cooldown is 7 days (`default-days`).
- gosec findings are annotated in place (`// #nosec — reason`), never
  blanket-excluded per package. Generated web assets stay 0644/0755 on purpose.
- Semgrep rulesets `p/golang` + `p/security-audit` are the shared local/CI
  set; `--metrics=off` everywhere.
- Demo media pipeline mirrors Knowledge-BA46-Flow: `scripts/docs-demo.sh`
  screenshots `docs/assets/demo.html` steps → `scripts/screencast-convert.mjs`
  assembles `docs/assets/demo/*.{png,apng,webp,avif,svg}`; working frames in
  `data/` (gitignored).

## Working agreement

- **Documentation is part of the change.** Any change to behavior, config
  schema, flags, tools or workflow updates `README.md`, `CHANGELOG.md`,
  this file and any relevant `README.todo.*` — in the same commit, not as
  a follow-up.
- **`README.todo.<topic>`** files hold analysis + suggestions for future
  tasks/development (e.g. `README.todo.vectorcraft`). Create one when work
  is analyzed but deferred; check for existing ones before starting
  related work; keep them accurate and delete them once fully implemented.
- **Tests cover new code.** Every new function/feature gets unit coverage
  (temp dirs + fake tool stubs — see `render_test.go`/`edit_test.go`);
  extend `fuzz_test.go` or `integration_test.go` where the pattern fits.
  `go test -race ./...` must stay green.
- **Changelog:** every user-facing change lands in the top `CHANGELOG.md`
  section, keeping the `VERSION`/`package.json` sync rules above.
- **Best practice:** smallest correct change, idiomatic Go, follow this
  file's conventions; fail loudly rather than silently falling back.

## Known pitfalls

- resvg renders with system fonts — ship fonts in `assets/fonts/`
  (auto-detected) and set `skipSystemFonts` for cross-machine reproducibility.
- Image paths in configs resolve against the working directory, not the
  config file's directory — run from the repo root.
- `ffmpeg` on this host has no `libwebp`/`libaom`; WebP goes through
  `img2webp`, AVIF through `libsvtav1`. Both are build-host tools, not Go deps.
- `.semgrepignore` excludes `demo/`, `assets/`, `output/`; `data/` frames are
  regenerated, never committed.

## MCP servers & global knowledge

Use the configured MCP servers proactively during tasks — they carry
project context the repo alone does not.

- **`knowledge-base`** — indexed engineering corpus
  (<https://github.com/gundestrup/engineering-memory>). Query with
  `retrieve_knowledge`/`ask_knowledge` for "what do we know about X"
  questions before browsing the repo or the web.
- **`wvf`** — website-vitals monitoring (the sibling
  Knowledge-BA46-Flow stack that these logos brand). `scan` a URL for
  core web vitals + http checks (`deep` adds attribution + lighthouse;
  `only` focuses it — `http`, `h3`, `images`, `js`, `css`, `vitals`),
  `sites`/`history`/`run` list monitored sites and per-request run
  detail, `jobs` shows background scan state.
  - **HTML analysis & bug finding:** `scan` works on localhost too —
    point it at `make demo` (`:8080`) or `make edit` (`:8181`) after
    changing `internal/server/static/index.html`/`demo/` to catch broken links, 404'd
    resources, bad response headers and JS/CSS issues before they reach
    a browser. Use it for "how does site X perform" questions or to
    validate rendered output on live pages.

Relevant notes:

- `tools/agents-md.md`
- `tools/golangci-lint.md`
- `tools/gosec.md`
- `tools/semgrep.md`
- `tools/github-actions.md`
- `tools/biome.md`
- `tools/prettier.md`
- `tools/govulncheck.md`
- `languages/go.md`
- `patterns/go-testing.md`
- `patterns/version-consistency.md`
- `patterns/npm-supply-chain-hardening.md`
- `repos/Knowledge-BA46-Flow.md`
- `scripts/check-dom-refs.mjs` (in `npm run check`) fails when a literal
  `$("id")`/`getElementById("id")` lookup has no matching `id="…"` in the
  same HTML file — DOM ids referenced by JS must exist in markup.

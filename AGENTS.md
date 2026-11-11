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

| Topic                                | Document             |
| ------------------------------------ | -------------------- |
| Usage, config schema, flags, fonts   | `README.md`          |
| Current and unreleased changes       | `CHANGELOG.md`       |
| App version (SemVer, build-injected) | `VERSION`            |
| Agent permission policy              | `.devin/config.json` |

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

## Known pitfalls

- resvg renders with system fonts — ship fonts in `assets/fonts/`
  (auto-detected) and set `skipSystemFonts` for cross-machine reproducibility.
- Image paths in configs resolve against the working directory, not the
  config file's directory — run from the repo root.
- `ffmpeg` on this host has no `libwebp`/`libaom`; WebP goes through
  `img2webp`, AVIF through `libsvtav1`. Both are build-host tools, not Go deps.
- `.semgrepignore` excludes `demo/`, `assets/`, `output/`; `data/` frames are
  regenerated, never committed.

## Global knowledge

<https://github.com/gundestrup/engineering-memory>

If the `knowledge-base` MCP server is configured (user scope), query the
indexed corpus with `retrieve_knowledge`/`ask_knowledge` for "what do we know
about X" questions before browsing the repo.

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

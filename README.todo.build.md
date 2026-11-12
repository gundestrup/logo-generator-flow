# TODO — binary release builds

Tracks what's missing to ship compiled binaries. Today releases are
**source-only**: tags `v0.1.0`/`v0.1.1` exist on GitHub with release notes,
but no artifacts — users must `go build` themselves.

## Current state

| Piece               | Status                                                                                                          |
| ------------------- | --------------------------------------------------------------------------------------------------------------- |
| Binary              | pure Go, zero CGO → cross-compiles for any GOOS/GOARCH from one machine                                         |
| Version injection   | `-ldflags "-s -w -X main.version=$(VERSION)"` via `Makefile` / `make build`                                     |
| Version consistency | `scripts/check-version.sh` enforces VERSION ↔ package.json ↔ CHANGELOG (in `make check`)                        |
| Release trigger     | none — `ci.yml` runs on `push`/`pull_request` only; releases are made by hand (`git tag` + `gh release create`) |
| Build matrix        | none — CI tests on `golang:1.27.4-trixie` (linux/amd64 container) only                                          |
| Artifacts           | none — no GoReleaser config, no `dist/`, no checksums                                                           |

## Runtime dependency caveat

The binary shells out to external tools at render time:

- `resvg` (PNG), `cwebp` (WebP), `avifenc` (AVIF); JPEG uses Go stdlib
- **SVG-only output works standalone** — a shipped binary is still useful
  with zero tools installed (`"formats": ["svg"]` and the `-ui` editor,
  which previews SVG without any tools)
- Install docs must keep telling users how to get the tools
  (`brew install resvg webp libavif` on macOS; distro packages elsewhere)

## Proposed build matrix

6 targets, all cross-compiled with `CGO_ENABLED=0`:

| GOOS    | GOARCH | Notes                      |
| ------- | ------ | -------------------------- |
| darwin  | amd64  | Intel Macs                 |
| darwin  | arm64  | Apple Silicon              |
| linux   | amd64  | servers/CI                 |
| linux   | arm64  | arm servers, docker-on-arm |
| windows | amd64  | `.exe`, zip archive        |
| windows | arm64  | optional — drop if unused  |

Naming: `logo-generator_<ver>_<os>_<arch>.tar.gz` (`.zip` for windows) +
`checksums.txt`.

## Option A — GoReleaser (recommended)

Standard tool; handles matrix, archives, checksums, changelog-from-git,
and attaches everything to the GitHub Release on tag push.

- `.goreleaser.yml`: `builds` with `CGO_ENABLED=0`, ldflags
  `-s -w -X main.version={{.Version}}`, archives per matrix above
- `.github/workflows/release.yml` on `push: tags: "v*"`:
  `contents: write` permission, `actions/checkout` + `setup-go` +
  `goreleaser/goreleaser-action` — **pin every action to a commit SHA
  with a `# vN` comment and pin the GoReleaser version**, per house CI
  rules (`.github/workflows/ci.yml` is the reference)
- Optional extras: `cosign` checksum signing, Homebrew tap formula
  (`goreleaser` can push `gundestrup/homebrew-tap`), `.deb`/`.rpm` via
  `nfpms`, winget/choco manifests

## Option B — hand-rolled CI job (minimal)

One `release` job in `ci.yml` (or `release.yml`) on `push: tags: "v*"`:
loop the matrix with `go build -trimpath -buildvcs=false -ldflags`,
`tar`/`zip` + `shasum -a 256`, then `gh release upload` (github CLI is
preinstalled on `ubuntu-latest`). ~40 lines of YAML, no new tool, but you
maintain the matrix and changelog assembly yourself.

## Checklist when implementing

- [ ] Pick A or B; create the tag-triggered workflow (SHA-pinned actions)
- [ ] Confirm `contents: write` on the release job only (keep read-only elsewhere)
- [ ] Verify `main.version` is injected — `logo-generator -version` on a
      release binary must print the tag, not `dev`
- [ ] Test the produced binaries on at least darwin/arm64 and linux/amd64
      (`-version` + an svg-only render)
- [ ] Update `README.md` install section: download-binary path alongside
      `go build`, keeping the resvg/cwebp/avifenc instructions prominent
- [ ] `make version-check` still green; CHANGELOG release section dated
- [ ] (optional) checksum/cosign verification docs, Homebrew tap

APP_VERSION := $(shell tr -d '\r\n' < VERSION)
LDFLAGS := -s -w -X main.version=$(APP_VERSION)
BIN := logo-generator

.PHONY: all build run test test-race cover fmt fmt-check vet lint semgrep \
	vuln-check deps-check demo demo-media edit frontend-check version-check \
	install-hooks quality ready clean bump

build:
	go build -trimpath -buildvcs=false -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/logo-generator

run: build
	./$(BIN)

test:
	go test ./...

test-race:
	go test -race ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

fmt:
	gofmt -s -w .
	@if command -v goimports >/dev/null 2>&1; then goimports -w .; fi

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt needed" >&2; exit 1; }

vet:
	go vet ./...

# golangci-lint runs the curated analyzer set from .golangci.yml. It is a
# separate binary, not a Go tool dependency — install with
# `brew install golangci-lint` (or the CI action). Without the binary the
# target self-skips; LINT_REQUIRED=1 fails closed.
lint:
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "golangci-lint not installed — skipping (brew install golangci-lint)"; \
		test -z "$$LINT_REQUIRED" || exit 1; \
	else \
		golangci-lint run; \
	fi

# govulncheck reports reachable Go vulnerability advisories; needs network
# access to the Go vuln database, so it is separate from `check`.
vuln-check:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

# deps-check lists modules with updates available — review before each
# release bump.
deps-check:
	@go list -m -u all | grep '\[' || echo "all dependencies current"

# Semgrep SAST — same registry rulesets as the CI `semgrep` job. Needs the
# semgrep CLI (brew/pipx) and network access to semgrep.dev for rules.
# SEMGREP_SKIP=1 bypasses it in the pre-commit hook when offline.
semgrep:
	semgrep scan --config p/golang --config p/security-audit --metrics=off --timeout 30 --error

edit: build
	./$(BIN) -edit

demo: build
	./$(BIN)
	@printf '[' > demo/sites.json; \
	first=1; \
	for f in configs/*.json; do \
		[ $$first -eq 1 ] || printf ',' >> demo/sites.json; \
		printf '"%s"' "$$(basename $$f .json)" >> demo/sites.json; \
		first=0; \
	done; \
	printf ']\n' >> demo/sites.json
	@echo "Demo: http://localhost:8080/demo/"
	@python3 -m http.server 8080

# Rebuild docs/assets demo media: Chrome stills of the stepped mock
# assembled into png/apng/webp/avif/svg by scripts/screencast-convert.mjs.
demo-media:
	sh scripts/docs-demo.sh

frontend-check:
	npm run check

# VERSION vs package.json vs latest CHANGELOG section.
version-check:
	sh scripts/check-version.sh

# Release bump: make bump V=x.y.z — writes VERSION + package.json +
# README badge + CHANGELOG section, then runs version-check.
bump:
	@test -n "$(V)" || { echo "usage: make bump V=x.y.z" >&2; exit 1; }
	sh scripts/bump-version.sh $(V)

install-hooks:
	git config core.hooksPath hooks

# Full local quality gate — same checks CI runs.
check: version-check fmt-check vet lint test-race build

# Everything locally verifiable in one command — deterministic checks
# first, network-dependent scans last.
ready: check frontend-check deps-check semgrep vuln-check

clean:
	rm -f $(BIN) coverage.out coverage.html
	rm -rf output/*/

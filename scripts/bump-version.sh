#!/bin/sh
# Release bump: VERSION is the single source of truth — this script copies it
# into the places that must hold it statically:
#   package.json   — npm needs a real field, it can't read VERSION
#   README badge   — shields.io URL has the number baked in
#   CHANGELOG      — "## [Unreleased]" becomes "## [x.y.z] - YYYY-MM-DD"
# Usage: sh scripts/bump-version.sh 0.3.0   (or: make bump V=0.3.0)
set -eu

v="${1:-}"
case "$v" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *)
    echo "usage: $0 MAJOR.MINOR.PATCH" >&2
    exit 1
    ;;
esac

# Preflight everything before writing — a half-applied bump is worse
# than no bump.
grep -q "## \[Unreleased\]" CHANGELOG.md ||
  { echo "CHANGELOG.md: no [Unreleased] section" >&2; exit 1; }
grep -q "## \[$v\]" CHANGELOG.md &&
  { echo "CHANGELOG.md: [$v] section already exists" >&2; exit 1; }
grep -q '"version"' package.json ||
  { echo "package.json: version field not found" >&2; exit 1; }
grep -qE 'version-[0-9]+\.[0-9]+\.[0-9]+' README.md ||
  { echo "README.md: version badge not found" >&2; exit 1; }

printf '%s\n' "$v" > VERSION

python3 - "$v" <<'PY'
import re, sys, time

v = sys.argv[1]
today = time.strftime("%Y-%m-%d")

p = open("package.json").read()
p = re.sub(r'("version":\s*")[^"]+(")', rf"\g<1>{v}\g<2>", p, count=1)
open("package.json", "w").write(p)

r = open("README.md").read()
r = re.sub(r"version-\d+\.\d+\.\d+", f"version-{v}", r, count=1)
open("README.md", "w").write(r)

c = open("CHANGELOG.md").read()
c = c.replace("## [Unreleased]",
              f"## [Unreleased]\n\n## [{v}] - {today}", 1)
open("CHANGELOG.md", "w").write(c)
PY

sh scripts/check-version.sh
echo "bumped to $v — review 'git diff', commit, tag v$v, push"

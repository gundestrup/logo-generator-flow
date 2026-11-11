#!/bin/sh
# Drift check: VERSION, package.json version, and the latest dated
# CHANGELOG section must agree. Optional arg: a release tag (vX.Y.Z)
# that must also match.
set -eu

fail() { echo "version-check: $1" >&2; exit 1; }

v=$(tr -d '\r\n' < VERSION)
case "$v" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) fail "VERSION '$v' is not MAJOR.MINOR.PATCH" ;;
esac

pkg=$(grep '"version"' package.json | head -1 | sed 's/.*"\([0-9][0-9.]*\)".*/\1/')
[ "$v" = "$pkg" ] || fail "VERSION ($v) != package.json ($pkg)"

latest=$(grep -oE '^## \[[0-9]+\.[0-9]+\.[0-9]+\]' CHANGELOG.md | head -1 | tr -d '[]# ')
[ "$v" = "$latest" ] || fail "VERSION ($v) != latest CHANGELOG section ($latest)"

badge=$(grep -oE 'version-[0-9]+\.[0-9]+\.[0-9]+' README.md | head -1 | sed 's/version-//')
[ "$v" = "$badge" ] || fail "VERSION ($v) != README badge ($badge)"

if [ $# -ge 1 ]; then
  [ "$1" = "v$v" ] || fail "release tag '$1' != VERSION v$v"
fi
echo "version-check: OK ($v)"

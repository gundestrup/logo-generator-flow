#!/bin/sh
# Copy pinned npm dist files into the embedded static dir (see
# tools/npm-vendoring.md — committed assets, never a runtime CDN).
set -eu
dst=internal/server/static/vendor
mkdir -p "$dst"
cp node_modules/bulma/css/bulma.min.css "$dst/bulma.min.css"
cp node_modules/@alpinejs/csp/dist/cdn.min.js "$dst/alpine-csp.min.js"
echo "vendored: bulma + alpine-csp -> $dst"

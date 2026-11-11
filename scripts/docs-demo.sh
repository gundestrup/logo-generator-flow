#!/bin/sh
# docs/assets demo captures.
#
# Screenshots the stepped mock in docs/assets/demo.html with headless
# Chrome (?step=0..3), then assembles the stills into the animated web
# formats via scripts/screencast-convert.mjs — the same toolchain as
# Knowledge-BA46-Flow.
#
# Build-host tools: go, node, Chrome (CHROME_BIN), ffmpeg (apng muxer +
# libsvtav1), img2webp, webpinfo.
set -eu

page="docs/assets/demo.html"
outdir="docs/assets/demo"
demo="logo-generator-flow"
steps=4
frames="data/demo-frames/$demo"
chrome="${CHROME_BIN:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"

[ -x "$chrome" ] || { echo "Chrome not found (set CHROME_BIN)" >&2; exit 1; }
for tool in go node ffmpeg img2webp webpinfo; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool not found" >&2; exit 1; }
done

# Real outputs feed the mock's <img> tags.
go build -trimpath -ldflags="-s -w" -o logo-generator .
./logo-generator

mkdir -p "$frames" "$outdir"
rm -f "$frames"/f*.png

i=0
while [ "$i" -lt "$steps" ]; do
  n=$(printf %04d "$i")
  if ! "$chrome" --headless=new --disable-gpu --hide-scrollbars \
    --force-device-scale-factor=1 \
    --screenshot="$frames/f$n.png" --window-size=880,560 \
    "file://$PWD/$page?step=$i" >/dev/null 2>&1; then
    echo "Chrome screenshot failed (step $i)" >&2
    exit 1
  fi
  i=$((i + 1))
done

# Poster = first frame (overview state).
cp "$frames/f0000.png" "$outdir/$demo.png"
node scripts/screencast-convert.mjs "$frames" \
  --name "$demo" --out "$outdir" --fps 0.5
echo "wrote $outdir/$demo.{png,apng,webp,avif,svg}"

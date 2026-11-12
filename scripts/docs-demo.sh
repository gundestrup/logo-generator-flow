#!/bin/sh
# docs/assets demo captures.
#
# Two animations, same toolchain as Knowledge-BA46-Flow:
#   docs/assets/demo/logo-generator-flow.* — pipeline overview: CLI block,
#     config → template → output, and a REAL editor screenshot (captured
#     live via scripts/capture-editor.mjs, so the demo can't drift from
#     the actual UI). Steps highlight each section (?step=0..3).
#   docs/assets/demo/logo-generator-cli.* — terminal recording of a full
#     render + the -ui launch, typed out progressively (?step=0..9).
#
# Both are stepped mocks screenshotted with headless Chrome, then
# assembled via scripts/screencast-convert.mjs. The editor still is a
# genuine capture of http://127.0.0.1:8181/ (starts -ui when needed).
#
# Build-host tools: go, node, Chrome (CHROME_BIN), ffmpeg (apng muxer +
# libsvtav1), img2webp, webpinfo.
set -eu

outdir="docs/assets/demo"
chrome="${CHROME_BIN:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
export CHROME_BIN="$chrome"

[ -x "$chrome" ] || { echo "Chrome not found (set CHROME_BIN)" >&2; exit 1; }
for tool in go node ffmpeg img2webp webpinfo curl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool not found" >&2; exit 1; }
done

# Real outputs feed the mock's <img> tags.
go build -trimpath -ldflags="-s -w" -o logo-generator ./cmd/logo-generator
./logo-generator

mkdir -p "$outdir"

# capture_frames <page> <steps> <name> <fps> <window>
capture_frames() {
  page="$1"; steps="$2"; name="$3"; fps="$4"; window="$5"
  frames="data/demo-frames/$name"
  mkdir -p "$frames"
  rm -f "$frames"/f*.png
  i=0
  while [ "$i" -lt "$steps" ]; do
    n=$(printf %04d "$i")
    if ! "$chrome" --headless=new --disable-gpu --hide-scrollbars \
      --force-device-scale-factor=1 \
      --screenshot="$frames/f$n.png" --window-size="$window" \
      "file://$PWD/$page?step=$i" >/dev/null 2>&1; then
      echo "Chrome screenshot failed ($page step $i)" >&2
      exit 1
    fi
    i=$((i + 1))
  done
  cp "$frames/f0000.png" "$outdir/$name.png"
  node scripts/screencast-convert.mjs "$frames" \
    --name "$name" --out "$outdir" --fps "$fps"
  echo "wrote $outdir/$name.{png,apng,webp,avif,svg}"
}

# The editor still is a real screenshot: reuse a running -ui if there is
# one, otherwise start our own and clean it up on exit.
ui_pid=""
if ! curl -sf http://127.0.0.1:8181/api/ping >/dev/null 2>&1; then
  ./logo-generator -ui >/dev/null 2>&1 &
  ui_pid=$!
  trap '[ -n "$ui_pid" ] && kill "$ui_pid" 2>/dev/null || true' EXIT
  i=0
  while ! curl -sf http://127.0.0.1:8181/api/ping >/dev/null 2>&1; do
    i=$((i + 1))
    [ "$i" -gt 40 ] && { echo "editor did not come up on :8181" >&2; exit 1; }
    sleep 0.25
  done
fi
mkdir -p data/demo-frames
node scripts/capture-editor.mjs data/demo-frames/editor.png --size 1240x760

capture_frames "docs/assets/demo.html" 4 "logo-generator-flow" 0.5 880,880
capture_frames "docs/assets/cli.html" 10 "logo-generator-cli" 1.2 880,560

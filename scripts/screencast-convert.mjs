// Assemble a frames dir (f%04d.png stills) into the animated web formats
// used in docs/assets/demo/: apng, webp, avif, and a self-contained svg
// wrapper (base64-embedded animation, plays inside <img> without JS).
// Same pipeline as Knowledge-BA46-Flow's scripts/e2e/screencast-convert.mjs.
//
// Build-host tools: ffmpeg (apng muxer + libsvtav1), img2webp, webpinfo.
//
// Usage:
//   node scripts/screencast-convert.mjs <frames-dir>
//     [--name base] [--out docs/assets/demo] [--fps 0.5]
//     [--scale 880] [--crf 35] [--quality 80] [--preset 4]
//     [--svg-payload webp|avif]

import { spawnSync } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";

const positional = [];
const opts = {};
const argv = process.argv.slice(2);
for (let i = 0; i < argv.length; i++) {
  if (argv[i].startsWith("--")) opts[argv[i].slice(2)] = argv[++i];
  else positional.push(argv[i]);
}

const src = resolve(positional[0] ?? "");
if (!positional[0] || !existsSync(src) || !statSync(src).isDirectory()) {
  console.error(
    "usage: screencast-convert.mjs <frames-dir> [--name base] [--out docs/assets/demo] " +
      "[--fps 0.5] [--scale 880] [--crf 35] [--quality 80] [--preset 4] [--svg-payload webp|avif]",
  );
  process.exit(2);
}

const fps = Number(opts.fps ?? 0.5);
const scale = Number(opts.scale ?? 880);
const crf = Number(opts.crf ?? 35);
const quality = Number(opts.quality ?? 80);
// SVT-AV1 preset: lower = slower + smaller at the same crf.
const preset = Number(opts.preset ?? 4);
const outDir = resolve(opts.out ?? "docs/assets/demo");
const name = opts.name ?? basename(src);

const run = (bin, args, label) => {
  const res = spawnSync(bin, args, { encoding: "utf8" });
  if (res.status !== 0)
    throw new Error(`${label} failed: ${res.stderr || res.stdout}`);
};

// Uniform scaled PNG sequence regardless of source frame size.
const work = mkdtempSync(join(tmpdir(), "demo-convert-"));
const scaledDir = join(work, "scaled");
mkdirSync(scaledDir, { recursive: true });
run(
  "ffmpeg",
  [
    "-hide_banner",
    "-loglevel",
    "error",
    "-y",
    "-framerate",
    String(fps),
    "-i",
    join(src, "f%04d.png"),
    "-vf",
    `scale=${scale}:-2`,
    join(scaledDir, "f%04d.png"),
  ],
  "frames rescale",
);
const frames = readdirSync(scaledDir)
  .filter((f) => f.endsWith(".png"))
  .sort()
  .map((f) => join(scaledDir, f));
if (frames.length === 0) throw new Error(`no frames produced from ${src}`);

mkdirSync(outDir, { recursive: true });
const base = join(outDir, name);

// 1. APNG — lossless frames straight through ffmpeg's apng muxer.
run(
  "ffmpeg",
  [
    "-hide_banner",
    "-loglevel",
    "error",
    "-y",
    "-framerate",
    String(fps),
    "-i",
    join(scaledDir, "f%04d.png"),
    "-plays",
    "0",
    "-f",
    "apng",
    `${base}.apng`,
  ],
  "apng",
);

// 2. Animated WebP — img2webp (no libwebp in this ffmpeg). -d is the
//    per-frame delay in ms matching the frame rate.
run(
  "img2webp",
  [
    "-loop",
    "0",
    "-d",
    String(Math.round(1000 / fps)),
    "-lossy",
    "-q",
    String(quality),
    "-m",
    "6",
    ...frames,
    "-o",
    `${base}.webp`,
  ],
  "img2webp",
);

// 3. Animated AVIF — ffmpeg's avif muxer + SVT-AV1 sequence encode.
run(
  "ffmpeg",
  [
    "-hide_banner",
    "-loglevel",
    "error",
    "-y",
    "-framerate",
    String(fps),
    "-i",
    join(scaledDir, "f%04d.png"),
    "-c:v",
    "libsvtav1",
    "-crf",
    String(crf),
    "-preset",
    String(preset),
    "-pix_fmt",
    "yuv420p",
    `${base}.avif`,
  ],
  "avif",
);

// 4. SVG — self-contained wrapper around a raster animation (animates
//    inside <img>; needs no JS, unlike foreignObject/script players).
const svgPayload = opts["svg-payload"] === "avif" ? "avif" : "webp";
const info = spawnSync("webpinfo", ["-summary", `${base}.webp`], {
  encoding: "utf8",
}).stdout;
const dims = info.match(/Canvas size\s*:\s*(\d+) x (\d+)/) ?? [0, 880, 560];
const b64 = readFileSync(`${base}.${svgPayload}`).toString("base64");
writeFileSync(
  `${base}.svg`,
  `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 ${dims[1]} ${dims[2]}" width="${dims[1]}" height="${dims[2]}" role="img" aria-label="${name} screencast">
  <image xlink:href="data:image/${svgPayload};base64,${b64}" width="${dims[1]}" height="${dims[2]}"/>
</svg>
`,
);

console.log(
  `${frames.length} frames @ ${fps}fps -> ${base}.{apng,webp,avif,svg}`,
);

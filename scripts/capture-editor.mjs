// Screenshot the live editor UI via headless Chrome + CDP.
// Spawns Chrome itself (CHROME_BIN or the macOS default), picks a site that
// has region overlays (first one that does — ?site= deep-link), clicks a
// text region to show the green selection, and captures the window.
//
// Usage:
//   node scripts/capture-editor.mjs <out.png>
//     [--url http://127.0.0.1:8181] [--site name] [--size 1240x760]
//     [--click .region.txt]
//
// Expects the editor (-ui) to be running on --url.

import { spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const positional = [];
const opts = {};
const argv = process.argv.slice(2);
for (let i = 0; i < argv.length; i++) {
  if (argv[i].startsWith("--")) opts[argv[i].slice(2)] = argv[++i];
  else positional.push(argv[i]);
}
const out = positional[0];
if (!out) {
  console.error(
    "usage: capture-editor.mjs <out.png> [--url …] [--site …] [--size WxH] [--click sel]",
  );
  process.exit(2);
}
const base = opts.url ?? "http://127.0.0.1:8181";
const [W, H] = (opts.size ?? "1240x760").split("x").map(Number);
const chrome =
  process.env.CHROME_BIN ??
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

// --- spawn chrome, learn its debug port from stderr -------------------------
const profile = mkdtempSync(join(tmpdir(), "cap-editor-"));
const proc = spawn(chrome, [
  "--headless=new",
  "--disable-gpu",
  "--hide-scrollbars",
  "--force-device-scale-factor=1",
  "--no-first-run",
  `--user-data-dir=${profile}`,
  `--window-size=${W},${H}`,
  "--remote-debugging-port=0",
  "about:blank",
]);
const port = await new Promise((res, rej) => {
  let buf = "";
  const to = setTimeout(
    () => rej(new Error("chrome: no DevTools line")),
    15000,
  );
  proc.stderr.on("data", (d) => {
    buf += d;
    const m = buf.match(/DevTools listening on ws:\/\/[^:]+:(\d+)/);
    if (m) {
      clearTimeout(to);
      res(Number(m[1]));
    }
  });
  proc.on("exit", () => rej(new Error("chrome exited early")));
});

// --- CDP plumbing -----------------------------------------------------------
let id = 0;
const pending = new Map();
const wsUrl = await fetch(`http://127.0.0.1:${port}/json/list`)
  .then((r) => r.json())
  .then((t) => t.find((x) => x.type === "page").webSocketDebuggerUrl);
const ws = new WebSocket(wsUrl);
await new Promise((res) => (ws.onopen = res));
ws.onmessage = (m) => {
  const d = JSON.parse(m.data);
  if (d.id && pending.has(d.id)) {
    pending.get(d.id)(d);
    pending.delete(d.id);
  }
};
const send = (method, params = {}) =>
  new Promise((res) => {
    const i = ++id;
    pending.set(i, res);
    ws.send(JSON.stringify({ id: i, method, params }));
  });
const ev = async (expression) =>
  send("Runtime.evaluate", { expression, returnByValue: true }).then(
    (r) => r.result?.result?.value,
  );
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// --- pick a site ------------------------------------------------------------
const sites = await fetch(`${base}/api/sites`).then((r) => r.json());
const wanted =
  opts.site && sites.sites.includes(opts.site) ? [opts.site] : sites.sites;

await send("Page.enable");
await send("Runtime.enable");
let picked = null;
for (const s of wanted) {
  await send("Page.navigate", {
    url: `${base}/?site=${encodeURIComponent(s)}`,
  });
  // wait for alpine + loadSite: regions exist, or the template svg landed
  for (let i = 0; i < 40; i++) {
    const n = await ev(
      `document.querySelectorAll('.region').length + (document.querySelector('#tpl svg') ? 0.5 : 0)`,
    );
    if (n >= 1) break;
    await sleep(250);
  }
  const regions = await ev(`document.querySelectorAll('.region').length`);
  picked = s;
  if (regions > 0) break;
}

// showcase state: select a text region (green highlight) if any exist
await ev(`document.querySelector('${opts.click ?? ".region.txt"}')?.click()`);
await sleep(400);
await sleep(600);

const shot = await send("Page.captureScreenshot", { format: "png" });
writeFileSync(out, Buffer.from(shot.result.data, "base64"));
console.log(`captured ${picked} → ${out}`);

proc.kill("SIGTERM");
await new Promise((r) => {
  proc.on("exit", r);
  setTimeout(r, 3000); // don't hang if chrome ignores SIGTERM
});
rmSync(profile, { recursive: true, force: true, maxRetries: 3 });
process.exit(0);

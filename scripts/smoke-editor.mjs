#!/usr/bin/env node
// Editor smoke test — real headless-Chrome CDP session against the built
// binary. Catches the bug class Go tests can't see: alpine-csp directive
// errors, async state races, modal behavior, zero-tolerance console errors.
//
//   make smoke          (builds ./logo-generator, then runs this)
//   node scripts/smoke-editor.mjs [--url http://127.0.0.1:8181] [--site acme]
//
// Env: CHROME_BIN overrides browser discovery, SMOKE_SITE sets the site.
import { spawn, execSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

const args = process.argv.slice(2);
const opt = (name, dflt) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args[i + 1] : dflt;
};
const SITE = process.env.SMOKE_SITE || opt("site", "acme");
const BIN = opt("bin", "./logo-generator");
const PORT = 8181;
const BASE = `http://127.0.0.1:${PORT}`;

// ---- helpers ---------------------------------------------------------------
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const fails = [];
const check = (name, ok, detail = "") => {
  console.log(
    `${ok ? "PASS" : "FAIL"}  ${name}${detail ? "  — " + detail : ""}`,
  );
  if (!ok) fails.push(name);
};

function chromeBin() {
  const c = [
    process.env.CHROME_BIN,
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "google-chrome",
    "google-chrome-stable",
    "chromium",
    "chromium-browser",
  ].filter(Boolean);
  for (const b of c) {
    try {
      execSync(`"${b}" --version`, { stdio: "ignore" });
      return b;
    } catch {
      /* next */
    }
  }
  throw new Error("no Chrome/Chromium found — set CHROME_BIN");
}

// ---- editor server ---------------------------------------------------------
const server = spawn(BIN, ["-ui"], { stdio: ["ignore", "pipe", "pipe"] });
const srvOut = [];
server.stdout.on("data", (d) => srvOut.push(String(d)));
server.stderr.on("data", (d) => srvOut.push(String(d)));

// ---- chrome + cdp ----------------------------------------------------------
const chrome = spawn(
  chromeBin(),
  [
    "--headless=new",
    "--disable-gpu",
    "--no-sandbox",
    "--disable-dev-shm-usage",
    "--no-first-run",
    `--user-data-dir=${mkdtempSync(join(tmpdir(), "smoke-"))}`,
    "--remote-debugging-port=0",
    "about:blank",
  ],
  { stdio: ["ignore", "ignore", "pipe"] },
);
const wsPort = await new Promise((res, rej) => {
  let b = "";
  chrome.stderr.on("data", (d) => {
    b += d;
    const m = b.match(/DevTools listening on ws:\/\/[^:/]+:(\d+)\//);
    if (m) res(+m[1]);
  });
  setTimeout(() => rej(new Error("chrome debug port timeout")), 15000);
});

let msgId = 0;
const pending = new Map();
const jsErrors = [];
// Chrome prints "DevTools listening" before the HTTP endpoint accepts
// connections — poll /json/list rather than racing a single fetch.
// nosemgrep: typescript.react.security.react-insecure-request.react-insecure-request -- loopback-only CDP endpoint
let pageTarget;
for (let i = 0; i < 60; i++) {
  try {
    const targets = await fetch(`http://127.0.0.1:${wsPort}/json/list`).then(
      (r) => r.json(),
    );
    pageTarget = targets.find((x) => x.type === "page");
    if (pageTarget) break;
  } catch {
    /* endpoint not up yet — retry */
  }
  await new Promise((r) => setTimeout(r, 250));
}
if (!pageTarget) throw new Error("chrome devtools endpoint never came up");
const wsUrl = pageTarget.webSocketDebuggerUrl;
const ws = new WebSocket(wsUrl);
await new Promise((r) => (ws.onopen = r));
ws.onmessage = (m) => {
  const d = JSON.parse(m.data);
  if (d.id && pending.has(d.id)) {
    pending.get(d.id)(d);
    pending.delete(d.id);
  }
  if (d.method === "Runtime.exceptionThrown")
    jsErrors.push(
      (
        d.params.exceptionDetails.exception?.description ||
        d.params.exceptionDetails.text ||
        ""
      ).slice(0, 200),
    );
  if (d.method === "Runtime.consoleAPICalled" && d.params.type === "error")
    jsErrors.push(
      "console.error: " +
        d.params.args
          .map((a) => a.value ?? a.description ?? "")
          .join(" ")
          .slice(0, 200),
    );
};
const send = (method, params = {}) =>
  new Promise((res) => {
    const i = ++msgId;
    pending.set(i, res);
    ws.send(JSON.stringify({ id: i, method, params }));
  });
const ev = async (expression) => {
  const r = await send("Runtime.evaluate", {
    expression,
    returnByValue: true,
    awaitPromise: true,
  });
  if (r.result?.exceptionDetails)
    jsErrors.push(
      "eval: " +
        (r.result.exceptionDetails.exception?.description || "").slice(0, 200),
    );
  return r.result?.result?.value;
};
const app = `Alpine.$data(document.body)`;
const modal = (id) =>
  `(document.getElementById('${id}')&&getComputedStyle(document.getElementById('${id}')).display)`;

try {
  // wait for editor
  for (let i = 0; i < 60; i++) {
    // nosemgrep: typescript.react.security.react-insecure-request.react-insecure-request -- loopback-only smoke test; BASE is always 127.0.0.1
    const up = await fetch(`${BASE}/api/ping`)
      .then((r) => r.ok)
      .catch(() => false);
    if (up) break;
    if (i === 59) throw new Error("editor never came up: " + srvOut.join(""));
    await sleep(250);
  }

  await send("Runtime.enable");
  await send("Page.navigate", { url: `${BASE}/?site=${SITE}` });
  await sleep(3000);

  // -- 1. site + config load --------------------------------------------------
  const init = await ev(
    `(()=>{const a=${app};return {site:a.site,cfg:(a.configText||'').length,fonts:(a.fonts||[]).length}})()`,
  );
  check(
    "site loads",
    init?.site === SITE && init.cfg > 100,
    JSON.stringify(init),
  );

  // -- 2. global font row -----------------------------------------------------
  const font = await ev(
    `(()=>{const a=${app};const n=document.querySelector('.columns .font-none');const p=document.querySelector('.columns .font-prev');return {sel:a.fontSel,noneVisible:n&&!n.hidden&&getComputedStyle(n).display!=='none',prevVisible:p&&!p.hidden&&getComputedStyle(p).display!=='none'}})()`,
  );
  check(
    "font row shows selection or placeholder",
    font && (font.sel ? font.prevVisible : font.noneVisible),
    JSON.stringify(font),
  );

  // -- 3. font modal: open, Escape, backdrop, row pick -------------------------
  await ev(`${app}.openFontPick('global')`);
  await sleep(700);
  const rows = await ev(
    `document.querySelectorAll('#font-modal .frow').length`,
  );
  check(
    "font modal opens with rows",
    (await ev(modal("font-modal"))) === "grid" && rows > 0,
    `${rows} rows`,
  );
  await ev(
    `window.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}))`,
  );
  await sleep(300);
  check("Escape closes font modal", (await ev(modal("font-modal"))) === "none");
  await ev(`${app}.openFontPick('global')`);
  await sleep(400);
  await ev(
    `document.getElementById('font-modal').dispatchEvent(new MouseEvent('click',{bubbles:true}))`,
  );
  await sleep(300);
  check(
    "backdrop closes font modal",
    (await ev(modal("font-modal"))) === "none",
  );
  await ev(`${app}.openFontPick('global')`);
  await sleep(400);
  await ev(`document.querySelector('#font-modal .frow')?.click()`);
  await sleep(400);
  const picked = await ev(
    `(()=>{const a=${app};return {sel:a.fontSel,closed:getComputedStyle(document.getElementById('font-modal')).display}})()`,
  );
  check(
    "row pick selects + closes",
    picked?.closed === "none" && !!picked.sel,
    picked?.sel,
  );

  // -- 4. image modal ----------------------------------------------------------
  await ev(`${app}.openPick()`);
  await sleep(700);
  check("image modal opens", (await ev(modal("modal"))) === "grid");
  await ev(
    `window.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}))`,
  );
  await sleep(300);
  check("Escape closes image modal", (await ev(modal("modal"))) === "none");
  await ev(`${app}.openPick()`);
  await sleep(400);
  await ev(
    `document.getElementById('modal').dispatchEvent(new MouseEvent('click',{bubbles:true}))`,
  );
  await sleep(300);
  check("backdrop closes image modal", (await ev(modal("modal"))) === "none");

  // -- 5. global settings: template select + formats + dirty save --------------
  const gs = await ev(
    `(()=>{const a=${app};const s=document.querySelector('#gs select');return {tpl:a.templatePath(),opts:s?.options.length}})()`,
  );
  check("template select populated", !!gs?.tpl && gs.opts >= 1, gs?.tpl);
  await ev(`${app}.fmtToggle('jpg')`);
  await sleep(400);
  const dirty = await ev(
    `(()=>{const a=${app};const b=[...document.querySelectorAll('.card .h2-tools button')].find(b=>b.textContent.trim()==='save');return {dirty:a.cfgDirty(),fmts:JSON.parse(a.configText).output?.formats,saveShown:b&&!b.hidden}})()`,
  );
  check(
    "format toggle marks dirty + save appears",
    dirty?.dirty === true &&
      dirty.saveShown === true &&
      dirty.fmts?.includes("jpg"),
    JSON.stringify(dirty?.fmts),
  );
  await ev(`${app}.fmtToggle('jpg')`); // revert — must not mutate the on-disk config

  // -- 6. template mapping -----------------------------------------------------
  await ev(`${app}.openMap()`);
  await sleep(900);
  const mp = await ev(
    `(()=>{const a=${app};return {open:a.mapOpen,els:(a.mapElements||[]).length,fps:Object.keys(a.mapFp||{}).length}})()`,
  );
  check(
    "map modal finds elements",
    mp?.open === true && mp.els >= 5,
    `${mp?.els} elements`,
  );
  await ev(`(()=>{const a=${app};a.mapOpen=false})()`);

  // -- 7. zero JS errors -------------------------------------------------------
  check(
    "zero console errors / exceptions",
    jsErrors.length === 0,
    jsErrors.slice(0, 3).join(" | "),
  );
} finally {
  chrome.kill();
  server.kill();
}

if (fails.length) {
  console.log(`\n${fails.length} smoke check(s) failed`);
  process.exit(1);
}
console.log("\nsmoke: all checks passed");

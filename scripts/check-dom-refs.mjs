#!/usr/bin/env node
// Cross-checks literal DOM id references against the ids actually defined
// in each HTML file. Catches the class of bug where JS does $("mk-color")
// but no element with that id exists — invisible to syntax linters.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const dirs = ["demo", "edit", "docs/assets"];
const files = dirs.flatMap((d) =>
  readdirSync(d)
    .filter((f) => f.endsWith(".html"))
    .map((f) => join(d, f)),
);

// Collectors: defined ids come from id="…" attributes anywhere in the
// file (markup or JS-built HTML strings both count as defined).
const ID_DEF = /\bid\s*=\s*["']([^"']+)["']/g;
// Literal lookups: $("id"), getElementById("id"), querySelector("#id").
const REFS = [
  /\$\(\s*["']([^"']+)["']\s*\)/g,
  /getElementById\(\s*["']([^"']+)["']\s*\)/g,
  /querySelector\(\s*["']#([A-Za-z][\w:.-]*)["']\s*\)/g,
];

let failures = 0;
for (const file of files) {
  const src = readFileSync(file, "utf8");
  const defined = new Set([...src.matchAll(ID_DEF)].map((m) => m[1]));
  const missing = new Set();
  for (const re of REFS) {
    for (const m of src.matchAll(re)) {
      if (!defined.has(m[1])) missing.add(m[1]);
    }
  }
  if (missing.size) {
    failures += missing.size;
    console.error(
      `${file}: referenced id(s) not defined: ${[...missing].join(", ")}`,
    );
  }
}
if (failures) {
  console.error(`dom-refs: ${failures} unresolved id reference(s)`);
  process.exit(1);
}
console.log(`dom-refs: OK (${files.length} files checked)`);

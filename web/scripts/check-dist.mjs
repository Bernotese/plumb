// Fails when a production build contains the mock API (T-006 review):
// the mock data, its demo login or its banner must never ship in the
// UI that the plumb binary embeds.
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

const dir = process.argv[2] ?? "dist";
const markers = [
  "correct-horse-battery-staple", // demo password of the mock
  "this build uses a mock of the Plumb API", // demo banner
  "ubuntu-26.04-server-cloudimg-amd64.img", // OpenAPI example data
  "examples.gen", // the generated examples module
];

async function* files(d) {
  for (const e of await readdir(d, { withFileTypes: true })) {
    const p = join(d, e.name);
    if (e.isDirectory()) yield* files(p);
    else yield p;
  }
}

const found = [];
for await (const f of files(dir)) {
  if (!/\.(js|css|html|json|map)$/.test(f)) continue;
  const text = await readFile(f, "utf8");
  for (const m of markers) if (text.includes(m)) found.push(`${f}: ${m}`);
}
if (found.length) {
  console.error("mock code in the production build:\n  " + found.join("\n  "));
  process.exit(1);
}
console.log(`${dir}: no mock code`);

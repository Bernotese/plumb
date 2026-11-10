// Writes the response examples of the OpenAPI contract to a JSON file:
// { "<operationId>": { "<status>": example } }. The mock API (T-006) is
// built from these, so the mock and the contract cannot drift apart.
import { mkdir, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { bundle, createConfig } from "@redocly/openapi-core";

const [ref, out] = process.argv.slice(2);
if (!ref || !out) {
  console.error("usage: gen-examples.mjs <openapi.yaml> <out.json>");
  process.exit(2);
}
const config = await createConfig({});
const { bundle: doc } = await bundle({ ref, config, dereference: true });
const result = {};
for (const item of Object.values(doc.parsed.paths ?? {})) {
  for (const op of Object.values(item)) {
    if (!op || typeof op !== "object" || !op.operationId) continue;
    for (const [status, res] of Object.entries(op.responses ?? {})) {
      for (const media of Object.values(res.content ?? {})) {
        if (media.example !== undefined) {
          (result[op.operationId] ??= {})[status] = media.example;
        }
      }
    }
  }
}
await mkdir(dirname(out), { recursive: true });
await writeFile(out, JSON.stringify(result, null, 2) + "\n");
console.log(`examples → ${out}`);

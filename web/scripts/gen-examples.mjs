// Writes the response examples of the OpenAPI contract to a JSON file:
// { "<operationId>": { "<status>": example } }. The mock API (T-006) is
// built from these, so the mock and the contract cannot drift apart.
// It also writes roles.gen.json next to lib/ (T-154): the minimum role of
// each operation by the D-342 rule, read from the same contract, so the UI
// has no second table of rights.
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { bundle, createConfig } from "@redocly/openapi-core";

const [ref, out] = process.argv.slice(2);
if (!ref || !out) {
  console.error("usage: gen-examples.mjs <openapi.yaml> <out.json>");
  process.exit(2);
}
const config = await createConfig({});
const { bundle: doc } = await bundle({ ref, config, dereference: true });
const result = {};
const roles = {};
const ORDER = ["read", "plan", "apply", "admin"];
/**
 * D-342: `x-plumb-role` if set; else the lowest token tier the operation
 * lists; an operation that lists only `session` needs `admin`; no security
 * at all (login) needs no role.
 */
function minRole(op) {
  if (op["x-plumb-role"]) return op["x-plumb-role"];
  const security = op.security ?? doc.parsed.security ?? [];
  if (security.length === 0) return "none";
  const tiers = security.flatMap((s) => s.token ?? []).filter((t) => ORDER.includes(t));
  if (tiers.length) return tiers.sort((a, b) => ORDER.indexOf(a) - ORDER.indexOf(b))[0];
  return "admin";
}
for (const item of Object.values(doc.parsed.paths ?? {})) {
  for (const op of Object.values(item)) {
    if (!op || typeof op !== "object" || !op.operationId) continue;
    roles[op.operationId] = minRole(op);
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
const rolesOut = join(dirname(out), "..", "roles.gen.json");
await writeFile(rolesOut, JSON.stringify(roles, null, 2) + "\n");
console.log(`roles → ${rolesOut}`);

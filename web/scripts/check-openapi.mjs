// Validates the OpenAPI contract:
// 1. OpenAPI 3.1 structure plus Redocly's recommended-strict rules (examples must match schemas).
// 2. Plumb rules: every response body has an example; every property name is snake_case.
// Exits non-zero on any finding.
import { bundle, createConfig, formatProblems, getTotals, lint } from "@redocly/openapi-core";

const ref = process.argv[2];
if (!ref) {
  console.error("usage: check-openapi.mjs <openapi.yaml>");
  process.exit(2);
}

const config = await createConfig({
  extends: ["recommended-strict"],
  rules: {
    // `Event` is only referenced from the text/event-stream description (OpenAPI 3.1 cannot
    // attach a schema to SSE frames); it still feeds the generated types.
    "no-unused-components": "off",
    // Login is intentionally public; every other operation declares security.
    "security-defined": {
      severity: "error",
      exceptions: [{ path: "/session", methods: ["post"] }],
    },
  },
});

const problems = await lint({ ref, config });
const totals = getTotals(problems);
formatProblems(problems, { format: "stylish", totals, version: "plumb" });

const { bundle: doc } = await bundle({ ref, config });
const spec = doc.parsed;
const errors = [];

const deref = (obj) => {
  if (obj && typeof obj.$ref === "string" && obj.$ref.startsWith("#/")) {
    return obj.$ref
      .slice(2)
      .split("/")
      .reduce((o, k) => o?.[k], spec);
  }
  return obj;
};

const METHODS = ["get", "put", "post", "delete", "patch"];
for (const [path, item] of Object.entries(spec.paths ?? {})) {
  for (const method of METHODS) {
    const op = item[method];
    if (!op) continue;
    for (const [status, raw] of Object.entries(op.responses ?? {})) {
      const res = deref(raw);
      for (const [type, media] of Object.entries(res?.content ?? {})) {
        if (media.example === undefined && media.examples === undefined) {
          errors.push(`${method.toUpperCase()} ${path} ${status} ${type}: response has no example`);
        }
      }
    }
  }
}

const SNAKE = /^[a-z][a-z0-9]*(_[a-z0-9]+)*$/;
const walkSchema = (schema, where) => {
  if (!schema || typeof schema !== "object") return;
  for (const name of Object.keys(schema.properties ?? {})) {
    if (!SNAKE.test(name)) errors.push(`${where}: property "${name}" is not snake_case`);
  }
  for (const [key, value] of Object.entries(schema)) {
    if (key === "example" || key === "examples") continue;
    if (Array.isArray(value)) value.forEach((v, i) => walkSchema(v, `${where}.${key}[${i}]`));
    else if (value && typeof value === "object") walkSchema(value, `${where}.${key}`);
  }
};
walkSchema(spec.components?.schemas, "components.schemas");
walkSchema(spec.paths, "paths");

for (const e of errors) console.error(`  error    plumb  ${e}`);
if (totals.errors + totals.warnings + errors.length > 0) process.exit(1);
console.log(`${ref}: valid`);

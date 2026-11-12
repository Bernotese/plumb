// The browser check of a Block document against internal/state/schema/block.json
// (D-261, D-264). It is a small JSON Schema evaluator for the keywords that
// block.json uses; an unknown keyword is an error, so a schema change that
// needs more fails the tests instead of passing silently. The server check
// stays the authority: it also checks the resource attributes, the parameter
// schema and the references, which the browser does not know.
import blockSchema from "../../../internal/state/schema/block.json";

export interface SchemaError {
  /** JSON Pointer of the instance that fails, as the server names it. */
  pointer: string;
  /** Where the editor shows the error: a deeper pointer when there is one (an unknown key). */
  at: string;
  message: string;
}

type Schema = boolean | { [k: string]: unknown };

/** Keywords that only describe; they never fail. */
const ANNOTATIONS = new Set(["$schema", "$id", "title", "description", "default", "examples"]);
const KEYWORDS = new Set([
  "type",
  "const",
  "enum",
  "required",
  "properties",
  "additionalProperties",
  "pattern",
  "minLength",
  "maxLength",
  "items",
  "minItems",
  "maxItems",
  "uniqueItems",
  "minProperties",
  "maxProperties",
]);

/** A JSON Pointer token: "~" and "/" escaped (RFC 6901). */
export function token(s: string | number): string {
  return String(s).replace(/~/g, "~0").replace(/\//g, "~1");
}

function typeOf(v: unknown): string {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  return typeof v; // "object", "string", "number", "boolean"
}

function hasType(v: unknown, t: string): boolean {
  const actual = typeOf(v);
  if (t === "integer") return actual === "number" && Number.isInteger(v);
  return actual === t;
}

const isObject = (v: unknown): v is Record<string, unknown> => typeOf(v) === "object";

function equal(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  const ta = typeOf(a);
  if (ta !== typeOf(b)) return false;
  if (ta === "array") {
    const x = a as unknown[];
    const y = b as unknown[];
    return x.length === y.length && x.every((v, i) => equal(v, y[i]));
  }
  if (ta === "object") {
    const x = a as Record<string, unknown>;
    const y = b as Record<string, unknown>;
    const kx = Object.keys(x);
    return kx.length === Object.keys(y).length && kx.every((k) => k in y && equal(x[k], y[k]));
  }
  return false;
}

/** Code points, as JSON Schema counts string length. */
const length = (s: string) => [...s].length;
const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

const patterns = new Map<string, RegExp>();
function regex(p: string): RegExp {
  let r = patterns.get(p);
  if (!r) patterns.set(p, (r = new RegExp(p, "u")));
  return r;
}

/** Checks a value against a schema and returns every failing leaf. */
export function check(schema: Schema, value: unknown, pointer = ""): SchemaError[] {
  if (schema === true) return [];
  if (schema === false) return [{ pointer, at: pointer, message: "No value is allowed here." }];
  for (const k of Object.keys(schema)) {
    if (!KEYWORDS.has(k) && !ANNOTATIONS.has(k)) throw new Error(`blockCheck: keyword ${k}`);
  }
  const out: SchemaError[] = [];
  const fail = (message: string, at = pointer) => out.push({ pointer, at, message });
  const s = schema as {
    type?: string | string[];
    const?: unknown;
    enum?: unknown[];
    required?: string[];
    properties?: Record<string, Schema>;
    additionalProperties?: Schema;
    pattern?: string;
    minLength?: number;
    maxLength?: number;
    items?: Schema;
    minItems?: number;
    maxItems?: number;
    uniqueItems?: boolean;
    minProperties?: number;
    maxProperties?: number;
  };

  if (s.type !== undefined) {
    const types = Array.isArray(s.type) ? s.type : [s.type];
    if (!types.some((t) => hasType(value, t))) {
      fail(`Expected ${types.join(" or ")}, found ${typeOf(value)}.`);
    }
  }
  if ("const" in s && !equal(value, s.const)) fail(`Must be ${JSON.stringify(s.const)}.`);
  if (s.enum && !s.enum.some((e) => equal(value, e)))
    fail(`Must be one of ${s.enum.map((e) => JSON.stringify(e)).join(", ")}.`);

  if (typeof value === "string") {
    if (s.minLength !== undefined && length(value) < s.minLength)
      fail(`Needs at least ${plural(s.minLength, "character")}.`);
    if (s.maxLength !== undefined && length(value) > s.maxLength)
      fail(`Allows at most ${plural(s.maxLength, "character")}.`);
    if (s.pattern !== undefined && !regex(s.pattern).test(value))
      fail(`Does not match the pattern ${s.pattern}.`);
  }

  if (Array.isArray(value)) {
    if (s.minItems !== undefined && value.length < s.minItems)
      fail(`Needs at least ${plural(s.minItems, "entry", "entries")}.`);
    if (s.maxItems !== undefined && value.length > s.maxItems)
      fail(`Allows at most ${plural(s.maxItems, "entry", "entries")}.`);
    if (s.uniqueItems) {
      for (let i = 1; i < value.length; i++) {
        const j = value.slice(0, i).findIndex((v) => equal(v, value[i]));
        if (j >= 0) {
          fail(`Entry ${i + 1} is the same as entry ${j + 1}.`, `${pointer}/${i}`);
          break;
        }
      }
    }
    if (s.items !== undefined) {
      value.forEach((v, i) => out.push(...check(s.items as Schema, v, `${pointer}/${i}`)));
    }
  }

  if (isObject(value)) {
    const keys = Object.keys(value);
    for (const r of s.required ?? []) {
      if (!(r in value)) fail(`The key "${r}" is missing.`);
    }
    if (s.minProperties !== undefined && keys.length < s.minProperties)
      fail(`Needs at least ${plural(s.minProperties, "key")}.`);
    if (s.maxProperties !== undefined && keys.length > s.maxProperties)
      fail(`Allows at most ${plural(s.maxProperties, "key")}.`);
    const props = s.properties ?? {};
    const extra: string[] = [];
    for (const k of keys) {
      const p = `${pointer}/${token(k)}`;
      if (k in props) out.push(...check(props[k] as Schema, value[k], p));
      else if (s.additionalProperties === false) extra.push(k);
      else if (s.additionalProperties !== undefined)
        out.push(...check(s.additionalProperties, value[k], p));
    }
    // The server reports all unknown keys as one error on the object.
    if (extra.length) {
      const known = Object.keys(props);
      fail(
        `Unknown key ${extra.map((k) => `"${k}"`).join(", ")}.` +
          (known.length ? ` Allowed: ${known.join(", ")}.` : ""),
        `${pointer}/${token(extra[0] as string)}`,
      );
    }
  }
  return out;
}

/** The schema errors of a Block document. */
export function checkBlock(doc: unknown): SchemaError[] {
  return check(blockSchema as Schema, doc).map((e) =>
    /^\/spec\/resources\/\d+$/.test(e.pointer) && /key/.test(e.message)
      ? { ...e, message: "A resource entry has exactly one type key, for example file:." }
      : e,
  );
}

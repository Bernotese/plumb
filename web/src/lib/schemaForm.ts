// Forms from JSON Schema (ARCHITECTURE §3: "The UI builds forms from it").
// Supported: string, multi-line text (contentMediaType text/plain, D-056),
// number, integer, boolean, enum, list of strings; default, required,
// minimum/maximum, minLength/maxLength, pattern, minItems/maxItems. A string
// with `writeOnly: true` is a secret parameter (D-088): the form holds a
// secret name and saves `{secret: name}`, never a literal.
// D-363: an object with properties is a nested form, an array of such
// objects a list of nested forms (add, remove, move), an object with only
// scalar `additionalProperties` key/value rows, up to depth 3. Every other
// schema (deeper, oneOf, arrays of numbers, ...) is a YAML field for that
// one parameter; the server checks it as before.
// Everything is a pure function, so the screens stay thin.
import { parseYaml, toYaml } from "./yaml";

export type FieldKind =
  | "text"
  | "textarea"
  | "number"
  | "integer"
  | "boolean"
  | "enum"
  | "list"
  | "secret"
  | "object"
  | "objects"
  | "map"
  | "yaml";

/** How deep nested forms go (D-363); a top-level parameter is depth 1. */
export const MAX_DEPTH = 3;

export interface Field {
  name: string;
  label: string;
  kind: FieldKind;
  required: boolean;
  description?: string;
  default?: unknown;
  options?: string[];
  min?: number;
  max?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  minItems?: number;
  maxItems?: number;
  /** The fields of an object, or of each item of an array of objects. */
  fields?: Field[];
  /** The value field of a key/value map. */
  value?: Field;
}

/** One item of an array of objects; `stored` keeps keys the form cannot show. */
export interface Item {
  key: number;
  values: Values;
  stored: Record<string, unknown>;
}

/** One row of a key/value map. */
export interface Row {
  key: number;
  name: string;
  value: FieldValue;
}

/**
 * The value of a field while the user edits it: text, a checkbox, a list
 * of strings, a nested form (null: an optional object that is not set),
 * items of an array of objects, or map rows. A YAML field holds its text.
 */
export type FieldValue = string | boolean | string[] | Values | null | Item[] | Row[];
export type Values = { [name: string]: FieldValue };

let nextKey = 0;
const key = () => nextKey++;

interface Prop {
  type?: string | string[];
  title?: string;
  description?: string;
  default?: unknown;
  enum?: unknown[];
  const?: unknown;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  minItems?: number;
  maxItems?: number;
  items?: Prop;
  contentMediaType?: string;
  writeOnly?: boolean;
  properties?: Record<string, unknown>;
  required?: string[];
  additionalProperties?: boolean | Prop;
}

interface ObjectSchema {
  properties?: Record<string, unknown>;
  required?: string[];
}

/** Keywords that a form cannot show faithfully: such a schema is a YAML field. */
const COMPOSED = ["oneOf", "anyOf", "allOf", "not", "if", "$ref", "patternProperties"];
const composed = (p: Prop) => COMPOSED.some((k) => k in (p as object));
const typeOf = (p: Prop) => (Array.isArray(p.type) ? p.type.find((t) => t !== "null") : p.type);
const SCALAR = ["string", "integer", "number", "boolean"];

/** A form object: properties, nothing composed, no schema for extra keys. */
function formObject(p: Prop | undefined): boolean {
  return (
    !!p &&
    typeOf(p) === "object" &&
    !composed(p) &&
    p.properties !== undefined &&
    typeof p.additionalProperties !== "object"
  );
}

function kindOf(p: Prop, depth: number): FieldKind {
  if (composed(p)) return "yaml";
  // A secret parameter wins over every other widget, also over an enum (QA T-036 F1).
  const t = Array.isArray(p.type) ? p.type.find((x) => x !== "null") : p.type;
  if (t === "string" && p.writeOnly === true) return "secret";
  if (Array.isArray(p.enum) && p.enum.every((e) => typeof e === "string")) return "enum";
  const type = Array.isArray(p.type) ? p.type.find((t) => t !== "null") : p.type;
  switch (type) {
    case "string":
      if (p.writeOnly === true) return "secret";
      return p.contentMediaType === "text/plain" ? "textarea" : "text";
    case "integer":
      return "integer";
    case "number":
      return "number";
    case "boolean":
      return "boolean";
    case "array":
      if (p.items && typeOf(p.items) === "string" && !composed(p.items) && !p.items.writeOnly)
        return "list";
      return formObject(p.items) && depth < MAX_DEPTH ? "objects" : "yaml";
    case "object": {
      if (depth >= MAX_DEPTH) return "yaml";
      if (formObject(p)) return "object";
      const extra = p.additionalProperties;
      if (
        p.properties === undefined &&
        typeof extra === "object" &&
        !composed(extra) &&
        SCALAR.includes(String(typeOf(extra))) &&
        !extra.writeOnly &&
        !extra.enum
      )
        return "map";
      return "yaml";
    }
    default:
      return "yaml";
  }
}

const ACRONYMS: Record<string, string> = {
  ip: "IP",
  ssh: "SSH",
  id: "ID",
  url: "URL",
  tls: "TLS",
  dns: "DNS",
  cpu: "CPU",
};

/** A label from a property name: "server_names" → "Server names", "ssh_keys" → "SSH keys". */
export function humanize(name: string): string {
  const words = name
    .split("_")
    .filter(Boolean)
    .map((w) => ACRONYMS[w] ?? w);
  const s = words.join(" ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** The fields of an object schema, in property order; `depth` 1 is the top. */
export function fieldsOf(schema: ObjectSchema | undefined, depth = 1): Field[] {
  const required = new Set(schema?.required ?? []);
  return Object.entries(schema?.properties ?? {}).map(([name, raw]) => {
    const p = (raw ?? {}) as Prop;
    const f: Field = {
      name,
      label: p.title ?? humanize(name),
      kind: kindOf(p, depth),
      required: required.has(name),
    };
    if (f.kind === "object") f.fields = fieldsOf(p, depth + 1);
    if (f.kind === "objects") f.fields = fieldsOf(p.items, depth + 1);
    if (f.kind === "map") {
      const v = p.additionalProperties as Prop;
      f.value = { name: "value", label: "Value", kind: kindOf(v, depth + 1), required: true };
      if (v.minLength !== undefined) f.value.minLength = v.minLength;
      if (v.maxLength !== undefined) f.value.maxLength = v.maxLength;
      if (v.pattern !== undefined) f.value.pattern = v.pattern;
      if (v.minimum !== undefined) f.value.min = v.minimum;
      if (v.maximum !== undefined) f.value.max = v.maximum;
    }
    if (p.description) f.description = p.description;
    if (p.default !== undefined) f.default = p.default;
    if (f.kind === "enum") f.options = (p.enum as string[]).slice();
    if (p.minimum !== undefined) f.min = p.minimum;
    if (p.maximum !== undefined) f.max = p.maximum;
    if (p.minLength !== undefined) f.minLength = p.minLength;
    if (p.maxLength !== undefined) f.maxLength = p.maxLength;
    if (p.pattern !== undefined) f.pattern = p.pattern;
    if (p.minItems !== undefined) f.minItems = p.minItems;
    if (p.maxItems !== undefined) f.maxItems = p.maxItems;
    return f;
  });
}

const isObject = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v);

/** A new item of an array of objects, with the defaults of its fields. */
export function newItem(f: Field, given: Record<string, unknown> = {}): Item {
  return { key: key(), values: initialValues(f.fields ?? [], given), stored: given };
}

/** A new, empty row of a key/value map. */
export function newRow(f: Field): Row {
  return { key: key(), name: "", value: f.value?.kind === "boolean" ? false : "" };
}

/** Form values from given parameters, else defaults, else empty. */
export function initialValues(fields: Field[], given: Record<string, unknown> = {}): Values {
  const out: Values = {};
  for (const f of fields) {
    const v = f.name in given ? given[f.name] : f.default;
    switch (f.kind) {
      case "object":
        // An optional object that is not set stays unset until the person adds it.
        out[f.name] = isObject(v)
          ? initialValues(f.fields ?? [], v)
          : f.required
            ? initialValues(f.fields ?? [], {})
            : null;
        break;
      case "objects":
        out[f.name] = Array.isArray(v) ? v.map((it) => newItem(f, isObject(it) ? it : {})) : [];
        break;
      case "map":
        out[f.name] = isObject(v)
          ? Object.entries(v).map(([name, val]) => ({
              key: key(),
              name,
              value: typeof val === "boolean" ? val : val === null ? "" : String(val),
            }))
          : [];
        break;
      case "yaml":
        out[f.name] = v === undefined ? "" : toYaml(v);
        break;
      case "boolean":
        out[f.name] = v === true;
        break;
      case "list":
        out[f.name] = Array.isArray(v) ? v.map(String) : [];
        break;
      case "secret":
        // Only a reference is a valid value; a default or a literal is not shown.
        out[f.name] = secretName(f.name in given ? given[f.name] : undefined) ?? "";
        break;
      default:
        out[f.name] = v === undefined || v === null ? "" : String(v);
    }
  }
  return out;
}

/**
 * Error text per field path; empty when the values are valid. A nested
 * field has the path of its parents: `servers/0/port`, `labels/2`.
 */
export function validate(fields: Field[], values: Values, at = ""): Record<string, string> {
  const errors: Record<string, string> = {};
  for (const f of fields) {
    const path = at + f.name;
    const v = values[f.name];
    switch (f.kind) {
      case "object":
        if (isValues(v)) Object.assign(errors, validate(f.fields ?? [], v, `${path}/`));
        else if (f.required) errors[path] = "This field is required.";
        break;
      case "objects": {
        const items = (v as Item[] | undefined) ?? [];
        const err = countError(f, items.length);
        if (err) errors[path] = err;
        items.forEach((it, i) =>
          Object.assign(errors, validate(f.fields ?? [], it.values, `${path}/${i}/`)),
        );
        break;
      }
      case "map": {
        const rows = (v as Row[] | undefined) ?? [];
        const err = countError(f, rows.length);
        if (err) errors[path] = err;
        const seen = new Set<string>();
        rows.forEach((r, i) => {
          const name = r.name.trim();
          if (name === "") errors[`${path}/${i}/name`] = "Enter a key.";
          else if (seen.has(name))
            errors[`${path}/${i}/name`] = `The key "${name}" is there twice.`;
          seen.add(name);
          const verr = f.value ? fieldError(f.value, r.value) : undefined;
          if (verr) errors[`${path}/${i}/value`] = verr;
        });
        break;
      }
      default: {
        const err = fieldError(f, v);
        if (err) errors[path] = err;
      }
    }
  }
  return errors;
}

function isValues(v: unknown): v is Values {
  return isObject(v);
}

function countError(f: Field, n: number): string | undefined {
  const min = f.minItems ?? (f.required ? 1 : 0);
  if (n < min) return min === 1 ? "Add at least one entry." : `Add at least ${min} entries.`;
  if (f.maxItems !== undefined && n > f.maxItems) return `Use at most ${f.maxItems} entries.`;
  return undefined;
}

function fieldError(f: Field, v: FieldValue | undefined): string | undefined {
  if (f.kind === "boolean") return undefined;
  if (f.kind === "yaml") {
    const s = typeof v === "string" ? v : "";
    if (s.trim() === "") return f.required ? "This field is required." : undefined;
    const e = parseYaml(s).errors[0];
    return e ? `YAML${e.line ? ` line ${e.line}` : ""}: ${e.message}` : undefined;
  }
  if (f.kind === "secret") {
    const s = typeof v === "string" ? v : "";
    if (s === "") return f.required ? "Choose a secret." : undefined;
    return SECRET_NAME.test(s) ? undefined : "Choose a secret.";
  }
  if (f.kind === "list") {
    const items = ((v as string[] | undefined) ?? []).map((s) => s.trim()).filter(Boolean);
    const min = f.minItems ?? (f.required ? 1 : 0);
    if (items.length < min)
      return min === 1 ? "Add at least one entry." : `Add at least ${min} entries.`;
    return undefined;
  }
  const s = typeof v === "string" ? v : "";
  if (s.trim() === "") return f.required ? "This field is required." : undefined;
  if (f.kind === "number" || f.kind === "integer") {
    const n = Number(s);
    if (!Number.isFinite(n)) return "Enter a number.";
    if (f.kind === "integer" && !Number.isInteger(n)) return "Enter a whole number.";
    if (f.min !== undefined && n < f.min) return `Use ${f.min} or more.`;
    if (f.max !== undefined && n > f.max) return `Use ${f.max} or less.`;
    return undefined;
  }
  if (f.kind === "enum" && !f.options?.includes(s)) return "Choose one of the options.";
  if (f.minLength !== undefined && s.length < f.minLength)
    return `Use at least ${f.minLength} characters.`;
  if (f.maxLength !== undefined && s.length > f.maxLength)
    return `Use at most ${f.maxLength} characters.`;
  if (f.pattern !== undefined) {
    let re: RegExp | undefined;
    try {
      re = new RegExp(f.pattern, "u");
    } catch {
      re = undefined; // a pattern the browser cannot read is checked by the server
    }
    if (re && !re.test(s)) return "The value has the wrong format.";
  }
  return undefined;
}

/** One typed scalar from its text (number, integer, boolean, text). */
function scalarParam(f: Field, v: FieldValue | undefined): unknown {
  if (f.kind === "boolean") return v === true;
  const s = typeof v === "string" ? v : "";
  if (f.kind === "number" || f.kind === "integer") return Number(s.trim());
  return s;
}

/**
 * Parameters from valid form values: typed, empty optional values left
 * out. `stored` is the stored object: keys of a nested object that the
 * form cannot show keep their stored value.
 */
export function toParams(
  fields: Field[],
  values: Values,
  stored: Record<string, unknown> = {},
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of fields) {
    const v = values[f.name];
    switch (f.kind) {
      case "object":
        if (isValues(v)) {
          const old = stored[f.name];
          out[f.name] = mergeParams(isObject(old) ? old : {}, f.fields ?? [], v);
        }
        break;
      case "objects": {
        const items = (v as Item[] | undefined) ?? [];
        if (items.length || f.required)
          out[f.name] = items.map((it) => mergeParams(it.stored, f.fields ?? [], it.values));
        break;
      }
      case "map": {
        const rows = (v as Row[] | undefined) ?? [];
        if (rows.length || f.required)
          out[f.name] = Object.fromEntries(
            rows.map((r) => [r.name.trim(), f.value ? scalarParam(f.value, r.value) : r.value]),
          );
        break;
      }
      case "yaml": {
        const s = typeof v === "string" ? v : "";
        if (s.trim() !== "") out[f.name] = parseYaml(s).value;
        break;
      }
      case "boolean":
        out[f.name] = v === true;
        break;
      case "list": {
        const items = ((v as string[] | undefined) ?? []).map((s) => s.trim()).filter(Boolean);
        if (items.length || f.required) out[f.name] = items;
        break;
      }
      case "secret": {
        const s = typeof v === "string" ? v : "";
        if (s !== "") out[f.name] = { secret: s };
        break;
      }
      case "number":
      case "integer": {
        const s = typeof v === "string" ? v.trim() : "";
        if (s !== "") out[f.name] = Number(s);
        break;
      }
      default: {
        const s = typeof v === "string" ? v : "";
        if (s !== "" || f.required) out[f.name] = s;
      }
    }
  }
  return out;
}

/**
 * Parameters for a save: the stored object, with the fields that the form
 * owns replaced by the form values. Keys not in the schema keep their
 * stored value, so an edit never drops them silently.
 */
export function mergeParams(
  stored: Record<string, unknown>,
  fields: Field[],
  values: Values,
): Record<string, unknown> {
  const owned = new Set(fields.map((f) => f.name));
  const form = toParams(fields, values, stored);
  // Stored keys keep their order (a YAML view stays stable, D-363); new keys follow.
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(stored)) {
    if (!owned.has(k)) out[k] = v;
    else if (k in form) out[k] = form[k];
  }
  for (const [k, v] of Object.entries(form)) if (!(k in out)) out[k] = v;
  return out;
}

const SECRET_NAME = /^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$/;

/** The secret name of a stored `{secret: name}` reference, else undefined. */
export function secretName(v: unknown): string | undefined {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return undefined;
  const keys = Object.keys(v);
  const name = (v as { secret?: unknown }).secret;
  return keys.length === 1 && typeof name === "string" ? name : undefined;
}

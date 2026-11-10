// Forms from JSON Schema (ARCHITECTURE §3: "The UI builds forms from it").
// Supported: string, multi-line text (contentMediaType text/plain, D-056),
// number, integer, boolean, enum, list of strings; default, required,
// minimum/maximum, minLength/maxLength, pattern, minItems. A string with
// `writeOnly: true` is a secret parameter (D-088): the form holds a secret
// name and saves `{secret: name}`, never a literal.
// Everything is a pure function, so the screens stay thin.

export type FieldKind =
  | "text"
  | "textarea"
  | "number"
  | "integer"
  | "boolean"
  | "enum"
  | "list"
  | "secret"
  | "unsupported";

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
}

/** The value of a field while the user edits it. */
export type FieldValue = string | boolean | string[];
export type Values = Record<string, FieldValue>;

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
  items?: { type?: string };
  contentMediaType?: string;
  writeOnly?: boolean;
}

interface ObjectSchema {
  properties?: Record<string, unknown>;
  required?: string[];
}

function kindOf(p: Prop): FieldKind {
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
      return p.items?.type === "string" ? "list" : "unsupported";
    default:
      return "unsupported";
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

/** The fields of an object schema, in property order. */
export function fieldsOf(schema: ObjectSchema | undefined): Field[] {
  const required = new Set(schema?.required ?? []);
  return Object.entries(schema?.properties ?? {}).map(([name, raw]) => {
    const p = (raw ?? {}) as Prop;
    const f: Field = {
      name,
      label: p.title ?? humanize(name),
      kind: kindOf(p),
      required: required.has(name),
    };
    if (p.description) f.description = p.description;
    if (p.default !== undefined) f.default = p.default;
    if (f.kind === "enum") f.options = (p.enum as string[]).slice();
    if (p.minimum !== undefined) f.min = p.minimum;
    if (p.maximum !== undefined) f.max = p.maximum;
    if (p.minLength !== undefined) f.minLength = p.minLength;
    if (p.maxLength !== undefined) f.maxLength = p.maxLength;
    if (p.pattern !== undefined) f.pattern = p.pattern;
    if (p.minItems !== undefined) f.minItems = p.minItems;
    return f;
  });
}

/** Form values from given parameters, else defaults, else empty. */
export function initialValues(fields: Field[], given: Record<string, unknown> = {}): Values {
  const out: Values = {};
  for (const f of fields) {
    const v = f.name in given ? given[f.name] : f.default;
    switch (f.kind) {
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

/** Error text per field name; empty when the values are valid. */
export function validate(fields: Field[], values: Values): Record<string, string> {
  const errors: Record<string, string> = {};
  for (const f of fields) {
    const v = values[f.name];
    const err = fieldError(f, v);
    if (err) errors[f.name] = err;
  }
  return errors;
}

function fieldError(f: Field, v: FieldValue | undefined): string | undefined {
  if (f.kind === "unsupported" || f.kind === "boolean") return undefined;
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

/** Parameters from valid form values: typed, empty optional values left out. */
export function toParams(fields: Field[], values: Values): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of fields) {
    const v = values[f.name];
    switch (f.kind) {
      case "unsupported":
        break;
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
 * owns replaced by the form values. Keys the form cannot show (unsupported
 * types, keys not in the schema) keep their stored value, so an edit never
 * drops them silently.
 */
export function mergeParams(
  stored: Record<string, unknown>,
  fields: Field[],
  values: Values,
): Record<string, unknown> {
  const owned = new Set(fields.filter((f) => f.kind !== "unsupported").map((f) => f.name));
  const kept = Object.fromEntries(Object.entries(stored).filter(([k]) => !owned.has(k)));
  return { ...kept, ...toParams(fields, values) };
}

const SECRET_NAME = /^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$/;

/** The secret name of a stored `{secret: name}` reference, else undefined. */
export function secretName(v: unknown): string | undefined {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return undefined;
  const keys = Object.keys(v);
  const name = (v as { secret?: unknown }).secret;
  return keys.length === 1 && typeof name === "string" ? name : undefined;
}

// A small YAML writer for showing documents as text (T-034, D-110). The
// output is valid YAML and stable: keys keep the order of the API answer.
// Multi-line strings become literal blocks, so a config file diffs per line.
//
// parseYaml reads YAML that a person typed (the Block editor D-261, the YAML
// mode and YAML fields of D-363): YAML 1.2 core schema (`yes`, `no`, `on`,
// `off` stay strings; `0644` is the number 644), no anchors or aliases, no
// custom tags, one document only, like the import (D-095). The server
// checks the result again; this only finds the line of an error.
import { LineCounter, isMap, isNode, isPair, isScalar, isSeq, parseDocument, visit } from "yaml";

const PLAIN = /^[A-Za-z0-9_./][A-Za-z0-9 _./@+()-]*$/;
const RESERVED = /^(true|false|yes|no|on|off|null|~|y|n)$/i;
const NUMBER =
  /^[-+]?(\.?[0-9][0-9_]*(\.[0-9]*)?([eE][-+]?[0-9]+)?|0x[0-9a-fA-F_]+|0o[0-7_]+|0b[01_]+|\.inf|\.nan)$/i;
/** YAML 1.1 readers turn these into timestamps. */
const DATE = /^[0-9]{4}-[0-9]{1,2}-[0-9]{1,2}([Tt ].*)?$/;
/** Characters a literal block cannot hold: controls (not tab), NEL, LS, PS, BOM. */
// eslint-disable-next-line no-control-regex
const UNPRINTABLE = /[\u0000-\u0008\u000b-\u001f\u007f-\u009f\u2028\u2029\ufeff]/;

/** A string as a YAML scalar: plain when safe, else JSON-quoted (valid YAML). */
export function scalar(s: string): string {
  if (
    s !== "" &&
    PLAIN.test(s) &&
    !s.endsWith(" ") &&
    !RESERVED.test(s) &&
    !NUMBER.test(s) &&
    !DATE.test(s)
  ) {
    return s;
  }
  // JSON leaves DEL, the C1 controls and LS/PS raw; YAML wants them escaped.
  return JSON.stringify(s).replace(
    /[\u007f-\u009f\u2028\u2029]/g,
    (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, "0")}`,
  );
}

/**
 * Whether a string can be a literal block with a detected indentation: it
 * has a line break, no carriage return or unprintable character, its first
 * non-empty line does not start with a space or tab (that line sets the
 * indentation), and no line holds only spaces or tabs (such a line can be
 * longer than the indentation). Everything else is JSON-quoted.
 */
function isBlock(s: string): boolean {
  return (
    s.includes("\n") &&
    !s.includes("\r") &&
    !UNPRINTABLE.test(s) &&
    !/^\n*[ \t]/.test(s) &&
    !/^[ \t]+$/m.test(s) &&
    s.trim() !== ""
  );
}

function literal(s: string, indent: string): string[] {
  const chomp = s.endsWith("\n\n") ? "+" : s.endsWith("\n") ? "" : "-";
  const body = s.endsWith("\n") ? s.slice(0, -1) : s;
  return [`|${chomp}`, ...body.split("\n").map((l) => (l === "" ? "" : indent + l))];
}

function inline(v: unknown): string | undefined {
  if (v === null || v === undefined) return "null";
  if (typeof v === "boolean" || typeof v === "number") return String(v);
  if (typeof v === "string") return isBlock(v) ? undefined : scalar(v);
  if (Array.isArray(v)) return v.length === 0 ? "[]" : undefined;
  if (typeof v === "object") return Object.keys(v).length === 0 ? "{}" : undefined;
  return JSON.stringify(String(v));
}

/** Lines of `v` at `indent`; the first line is placed after a "key:" or "- ". */
function lines(v: unknown, indent: string): { head: string; rest: string[] } {
  const one = inline(v);
  if (one !== undefined) return { head: ` ${one}`, rest: [] };
  if (typeof v === "string") {
    const [first = "", ...more] = literal(v, indent + "  ");
    return { head: ` ${first}`, rest: more };
  }
  return { head: "", rest: block(v, indent + "  ") };
}

function block(v: unknown, indent: string): string[] {
  const out: string[] = [];
  if (Array.isArray(v)) {
    for (const item of v) {
      const one = inline(item);
      if (one !== undefined) {
        out.push(`${indent}- ${one}`);
      } else if (typeof item === "string") {
        const [first = "", ...more] = literal(item, indent + "  ");
        out.push(`${indent}- ${first}`, ...more);
      } else if (Array.isArray(item)) {
        const inner = block(item, indent + "  ");
        out.push(`${indent}- ${(inner[0] ?? "").trimStart()}`, ...inner.slice(1));
      } else {
        // An object item starts on the dash line: "- key: value".
        const inner = block(item, indent + "  ");
        out.push(`${indent}- ${(inner[0] ?? "").trimStart()}`, ...inner.slice(1));
      }
    }
    return out;
  }
  for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
    const { head, rest } = lines(val, indent);
    out.push(`${indent}${scalar(k)}:${head}`, ...rest);
  }
  return out;
}

/** The YAML text of a JSON value, ending with a newline. */
export function toYaml(v: unknown): string {
  const one = inline(v);
  if (one !== undefined) return `${one}\n`;
  if (typeof v === "string") return `${literal(v, "  ").join("\n")}\n`;
  return `${block(v, "").join("\n")}\n`;
}

/**
 * Keys that name JavaScript object internals. No document needs them; they
 * are refused in YAML text and in key/value rows (review of #278).
 */
export const FORBIDDEN_KEYS: readonly string[] = ["__proto__", "constructor", "prototype"];
export const forbiddenKey = (k: string) => `The key "${k}" is not allowed.`;

/** parseYaml refuses more text than this before it parses. */
export const MAX_TEXT = 256 * 1024;

export interface LineError {
  /** 1-based line; 0 when no line fits. */
  line: number;
  message: string;
  /** JSON Pointer into the document, when the error is about a value. */
  pointer?: string;
}

export interface Parsed {
  /** The document as JSON, or undefined when the YAML text does not parse. */
  value: unknown;
  /** YAML errors (and the errors a caller adds), sorted by line. */
  errors: LineError[];
  /** The line of a JSON Pointer: the line of the deepest key or entry that exists. */
  lineOf(pointer: string): number;
}

const NONE: Parsed = { value: undefined, errors: [], lineOf: () => 0 };

/** The tokens of a JSON Pointer (RFC 6901). */
export function tokens(pointer: string): string[] {
  if (pointer === "") return [];
  return pointer
    .slice(1)
    .split("/")
    .map((t) => t.replace(/~1/g, "/").replace(/~0/g, "~"));
}

/**
 * Parses YAML text strictly (see the top of this file). An empty text is
 * an error unless `allowEmpty`; then its value is undefined.
 */
export function parseYaml(text: string, opts: { allowEmpty?: boolean } = {}): Parsed {
  if (text.length > MAX_TEXT) {
    return {
      ...NONE,
      errors: [{ line: 0, message: `The text is longer than ${MAX_TEXT / 1024} KiB.` }],
    };
  }
  const lc = new LineCounter();
  const doc = parseDocument(text, {
    lineCounter: lc,
    version: "1.2",
    schema: "core",
    merge: false,
    uniqueKeys: true,
    prettyErrors: false,
    strict: true,
  });
  const lineAt = (offset: number | undefined) =>
    offset === undefined ? 0 : lc.linePos(offset).line;
  const nodeLine = (n: unknown): number =>
    isNode(n) && n.range ? lineAt(n.range[0]) : isPair(n) && isNode(n.key) ? nodeLine(n.key) : 0;

  const errors: LineError[] = [];
  for (const e of [...doc.errors, ...doc.warnings]) {
    errors.push({ line: e.linePos?.[0]?.line ?? lineAt(e.pos[0]), message: yamlMessage(e) });
  }
  visit(doc, {
    Alias(_, n) {
      errors.push({ line: nodeLine(n), message: "Aliases (*name) are not allowed." });
    },
    Node(_, n) {
      if (n.anchor) errors.push({ line: nodeLine(n), message: "Anchors (&name) are not allowed." });
    },
    Pair(_, p) {
      if (!isScalar(p.key) || (typeof p.key.value !== "string" && typeof p.key.value !== "number"))
        errors.push({ line: nodeLine(p), message: "A key must be a plain text." });
      else if (FORBIDDEN_KEYS.includes(String(p.key.value)))
        errors.push({ line: nodeLine(p), message: forbiddenKey(String(p.key.value)) });
    },
  });

  function lineOf(pointer: string): number {
    let node: unknown = doc.contents;
    let line = nodeLine(node) || 1;
    for (const t of tokens(pointer)) {
      if (isMap(node)) {
        const pair = node.items.find((p) => isScalar(p.key) && String(p.key.value) === t);
        if (!pair) break;
        line = nodeLine(pair.key) || line;
        node = pair.value;
      } else if (isSeq(node)) {
        const item = /^(0|[1-9][0-9]*)$/.test(t) ? node.items[Number(t)] : undefined;
        if (item === undefined) break;
        line = nodeLine(item) || line;
        node = item;
      } else break;
    }
    return line;
  }

  if (errors.length > 0) return { value: undefined, errors: sortErrors(errors), lineOf };
  const value: unknown = doc.toJS({ maxAliasCount: 0 });
  if ((value === null || value === undefined) && !(opts.allowEmpty && text.trim() === "")) {
    return { value, errors: [{ line: 1, message: "The text is empty." }], lineOf };
  }
  return { value: value ?? undefined, errors, lineOf };
}

export function sortErrors(errors: LineError[]): LineError[] {
  return errors.sort((a, b) => a.line - b.line);
}

/** The YAML library message without its code frame. */
function yamlMessage(e: { message: string }): string {
  const first = e.message.split("\n")[0] ?? e.message;
  return first.replace(/ at line \d+, column \d+:?$/, "").trim();
}

/** A document read in the YAML mode of a form (D-363). */
export interface YamlDoc {
  kind: string;
  name: string;
  spec: Record<string, unknown>;
}

/**
 * Parses the YAML mode text of one document and checks its shape: the
 * `kind`, the `name` (`name` when editing, else `namePattern`) and a
 * `spec` object. The server checks everything else, as for the form.
 */
export function parseDocText(
  text: string,
  want: { kind: string; name?: string; namePattern: RegExp },
): { parsed: Parsed; doc?: YamlDoc; errors: LineError[] } {
  const parsed = parseYaml(text);
  if (parsed.errors.length) return { parsed, errors: parsed.errors };
  const v = parsed.value as Record<string, unknown>;
  const errors: LineError[] = [];
  const at = (p: string, message: string) =>
    errors.push({ line: parsed.lineOf(p), pointer: p, message });
  if (typeof v !== "object" || v === null || Array.isArray(v)) {
    at("", `The text must be one ${want.kind} document (kind, name, spec).`);
    return { parsed, errors };
  }
  for (const k of Object.keys(v))
    if (!["kind", "name", "spec"].includes(k))
      at(`/${k}`, `Unknown key "${k}": use kind, name and spec.`);
  if (v["kind"] !== want.kind) at("/kind", `The kind must be ${want.kind}.`);
  if (typeof v["name"] !== "string") at("/name", "The name is missing.");
  else if (want.name !== undefined && v["name"] !== want.name)
    at("/name", `The name must stay "${want.name}".`);
  else if (want.name === undefined && !want.namePattern.test(v["name"]))
    at(
      "/name",
      "Use lower-case letters, digits, '-' and '.'; start and end with a letter or digit.",
    );
  const spec = v["spec"];
  if (typeof spec !== "object" || spec === null || Array.isArray(spec))
    at("/spec", "The spec must be a map.");
  if (errors.length) return { parsed, errors: sortErrors(errors) };
  return {
    parsed,
    doc: { kind: want.kind, name: v["name"] as string, spec: spec as Record<string, unknown> },
    errors,
  };
}

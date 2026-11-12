// Parse the YAML text of a Block in the browser (D-261, D-263) and find the
// line of a JSON Pointer, so a schema error or a 422 of the server shows at
// its line. YAML 1.2 core schema: `yes`, `no`, `on`, `off` stay strings and
// `0644` is the number 644 (quote a file mode). No anchors or aliases, no
// custom tags and one document only, like the import (D-095).
import { LineCounter, isMap, isNode, isPair, isScalar, isSeq, parseDocument, visit } from "yaml";
import { checkBlock } from "./blockCheck";

/** The editor refuses more text than this before it parses (a stored Block is far smaller). */
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
  /** YAML errors and schema errors, sorted by line. */
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

/** Parses the text, then checks it against block.json. */
export function parseBlock(text: string): Parsed {
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
  if (value === null || value === undefined) {
    return { value, errors: [{ line: 1, message: "The text is empty." }], lineOf };
  }
  for (const e of checkBlock(value)) {
    errors.push({ line: lineOf(e.at), message: e.message, pointer: e.pointer });
  }
  return { value, errors: sortErrors(errors), lineOf };
}

function sortErrors(errors: LineError[]): LineError[] {
  return errors.sort((a, b) => a.line - b.line);
}

/** The YAML library message without its code frame. */
function yamlMessage(e: { message: string }): string {
  const first = e.message.split("\n")[0] ?? e.message;
  return first.replace(/ at line \d+, column \d+:?$/, "").trim();
}

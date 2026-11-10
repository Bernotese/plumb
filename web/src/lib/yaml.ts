// A small YAML writer for showing documents as text (T-034, D-111). The
// output is valid YAML and stable: keys keep the order of the API answer.
// Multi-line strings become literal blocks, so a config file diffs per line.
// It is for display only; Plumb never parses this text.

const PLAIN = /^[A-Za-z0-9_./][A-Za-z0-9 _./@+()-]*$/;
const RESERVED = /^(true|false|yes|no|on|off|null|~|y|n)$/i;
const NUMBER =
  /^[-+]?(\.?[0-9][0-9_]*(\.[0-9]*)?([eE][-+]?[0-9]+)?|0x[0-9a-fA-F]+|0o[0-7]+|\.inf|\.nan)$/i;

/** A string as a YAML scalar: plain when safe, else JSON-quoted (valid YAML). */
export function scalar(s: string): string {
  if (s !== "" && PLAIN.test(s) && !s.endsWith(" ") && !RESERVED.test(s) && !NUMBER.test(s)) {
    return s;
  }
  return JSON.stringify(s);
}

function isBlock(s: string): boolean {
  return s.includes("\n") && !s.includes("\r") && !/^[ \t]/.test(s) && s.trim() !== "";
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

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// A text that a person sees names no decision, task or ARCHITECTURE number
// (D-123, T-123, § 5): those are for the team, not for the user. Code
// comments and test names keep them. The check reads every .svelte and .ts
// file of the UI (also the mock, whose texts the demo shows), drops the
// comments and fails on a number that is left in a template or a string.

const SRC = fileURLToPath(new URL("..", import.meta.url));
const NUMBER = /\b[DT]-\d{2,4}\b|§ ?\d+/g;

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n);
    if (statSync(p).isDirectory()) return files(p);
    if (!/\.(svelte|ts)$/.test(n) || /\.test\.ts$|\.gen\.ts$|testkit\.ts$/.test(n)) return [];
    return [p];
  });
}

/** The file without its comments: <!-- -->, block comments and line comments. */
export function withoutComments(text: string): string {
  return text
    .replace(/<!--[\s\S]*?-->/g, "")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/(^|[^:"'`\\])\/\/.*$/gm, "$1");
}

describe("user-visible texts", () => {
  it("name no decision, task or ARCHITECTURE number", () => {
    const hits: string[] = [];
    for (const f of files(SRC)) {
      const lines = withoutComments(readFileSync(f, "utf8")).split("\n");
      lines.forEach((l, i) => {
        for (const m of l.matchAll(NUMBER)) hits.push(`${f.slice(SRC.length)}:${i + 1}: ${m[0]}`);
      });
    }
    expect(hits).toEqual([]);
  });

  it("the comment filter keeps the texts and drops only comments", () => {
    const sample = [
      "// a comment (D-123)",
      "/* block (T-45) */ const a = 1;",
      "<!-- note (D-1) -->",
      'const url = "https://example.org/a"; // trailing (D-9)',
      "<p>Plumb never deletes one (D-233).</p>",
    ].join("\n");
    const left = withoutComments(sample);
    expect(left.match(NUMBER)).toEqual(["D-233"]);
    expect(left).toContain("https://example.org/a");
  });
});

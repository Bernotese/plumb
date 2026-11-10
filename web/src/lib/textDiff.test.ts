import { describe, expect, it } from "vitest";
import { diffLines, diffStats, splitLines, withContext, type DiffLine } from "./textDiff";

const ops = (d: DiffLine[]) =>
  d.map((l) => `${l.op === "same" ? " " : l.op === "add" ? "+" : "-"}${l.text}`);

/** Applying the diff to the old text must give the new text. */
function check(a: string, b: string) {
  const d = diffLines(a, b);
  const old = d.filter((l) => l.op !== "add").map((l) => l.text);
  const neu = d.filter((l) => l.op !== "del").map((l) => l.text);
  expect(old).toEqual(splitLines(a));
  expect(neu).toEqual(splitLines(b));
  // Line numbers count up without gaps.
  expect(d.filter((l) => l.old).map((l) => l.old)).toEqual(old.map((_, i) => i + 1));
  expect(d.filter((l) => l.new).map((l) => l.new)).toEqual(neu.map((_, i) => i + 1));
  return d;
}

describe("splitLines", () => {
  it("drops the empty line after a final newline", () => {
    expect(splitLines("")).toEqual([]);
    expect(splitLines("a\n")).toEqual(["a"]);
    expect(splitLines("a\n\n")).toEqual(["a", ""]);
    expect(splitLines("a\nb")).toEqual(["a", "b"]);
  });
});

describe("diffLines", () => {
  it("equal texts are all same", () => {
    expect(ops(check("a\nb\n", "a\nb\n"))).toEqual([" a", " b"]);
  });
  it("added and removed documents", () => {
    expect(ops(check("", "a\nb\n"))).toEqual(["+a", "+b"]);
    expect(ops(check("a\nb\n", ""))).toEqual(["-a", "-b"]);
    expect(check("", "")).toEqual([]);
  });
  it("a changed line is a removed line, then an added line", () => {
    expect(ops(check("a\nb\nc\n", "a\nB\nc\n"))).toEqual([" a", "-b", "+B", " c"]);
  });
  it("finds a minimal diff in the middle", () => {
    const d = check("x\na\nb\nc\nd\ny\n", "x\nb\nc\ne\nd\ny\n");
    expect(ops(d)).toEqual([" x", "-a", " b", " c", "+e", " d", " y"]);
    expect(diffStats(d)).toEqual({ added: 1, removed: 1 });
  });
  it("handles repeated lines", () => {
    check("a\na\nb\na\n", "a\nb\na\na\n");
    check("-\n-\n-\n", "-\n");
  });
  it("is correct on random texts", () => {
    let seed = 7;
    const rnd = () => (seed = (seed * 1103515245 + 12345) % 2 ** 31) % 4;
    for (let t = 0; t < 200; t++) {
      const a = Array.from({ length: rnd() * 3 }, () => "abcd"[rnd()]).join("\n");
      const b = Array.from({ length: rnd() * 3 }, () => "abcd"[rnd()]).join("\n");
      check(a, b);
    }
  });
  it("falls back on a very large middle and stays correct", () => {
    const a = Array.from({ length: 2500 }, (_, i) => `a${i}`).join("\n");
    const b = Array.from({ length: 2500 }, (_, i) => `b${i}`).join("\n");
    const d = check(`head\n${a}\ntail\n`, `head\n${b}\ntail\n`);
    expect(diffStats(d)).toEqual({ added: 2500, removed: 2500 });
  });
});

describe("withContext", () => {
  const text = (n: number) => Array.from({ length: n }, (_, i) => `l${i + 1}`).join("\n");
  it("keeps context lines and hides the rest", () => {
    const d = diffLines(text(20), text(20).replace("l10", "X"));
    const rows = withContext(d, 2);
    expect(rows[0]).toEqual({ op: "skip", count: 7 });
    expect(rows.slice(1, 7).map((r) => ("text" in r ? r.text : "?"))).toEqual([
      "l8",
      "l9",
      "l10",
      "X",
      "l11",
      "l12",
    ]);
    expect(rows[7]).toEqual({ op: "skip", count: 8 });
  });
  it("shows a single hidden line instead of a skip row", () => {
    const d = diffLines(text(4), text(4).replace("l4", "X"));
    expect(withContext(d, 2).some((r) => r.op === "skip")).toBe(false);
  });
  it("hides everything when nothing changed", () => {
    expect(withContext(diffLines(text(5), text(5)))).toEqual([{ op: "skip", count: 5 }]);
  });
});

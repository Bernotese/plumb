import { readFileSync, readdirSync } from "node:fs";
import { describe, expect, it } from "vitest";
import cases from "../../../internal/state/testdata/blockcheck/cases.json";
import blockSchema from "../../../internal/state/schema/block.json";
import { check, checkBlock } from "./blockCheck";
import { parseBlock } from "./blockYaml";

const pointers = (doc: unknown) => [...new Set(checkBlock(doc).map((e) => e.pointer))].sort();

// The same file runs through the server schema in internal/state/blockcheck_test.go (D-264).
describe("browser check agrees with the server cases", () => {
  it("has the cases", () => expect(cases.cases.length).toBeGreaterThanOrEqual(10));
  for (const c of cases.cases) {
    it(c.name, () => expect(pointers(c.doc)).toEqual([...c.pointers].sort()));
  }
});

describe("check", () => {
  it("accepts every starter Block", () => {
    const dir = new URL("../../../blocks/", import.meta.url);
    const files = readdirSync(dir).filter((f) => f.endsWith(".yaml"));
    expect(files.length).toBeGreaterThanOrEqual(5);
    for (const f of files) {
      const p = parseBlock(readFileSync(new URL(f, dir), "utf8"));
      expect(p.errors, f).toEqual([]);
    }
  });

  it("knows every keyword of block.json", () => {
    // check throws on an unknown keyword; walk every sub-schema of block.json.
    const walk = (s: unknown): void => {
      if (typeof s !== "object" || s === null) return;
      check(s as Record<string, unknown>, Symbol("no type matches"));
      for (const [k, v] of Object.entries(s)) {
        if (k === "properties") Object.values(v as object).forEach(walk);
        else if (k === "items" || k === "additionalProperties") walk(v);
      }
    };
    expect(() => walk(blockSchema)).not.toThrow();
    expect(() => check({ oneOf: [] }, 1)).toThrow(/oneOf/);
  });

  it("points an unknown key at the key itself", () => {
    const [e] = checkBlock({ kind: "Block", name: "a", spec: { resources: [{ f: {} }], x: 1 } });
    expect(e).toMatchObject({ pointer: "/spec", at: "/spec/x" });
    expect(e?.message).toContain('"x"');
  });

  it("names the one type key of a resource entry", () => {
    const errs = checkBlock({
      kind: "Block",
      name: "a",
      spec: { resources: [{}, { a: {}, b: {} }] },
    });
    expect(errs.map((e) => e.message)).toEqual([
      "A resource entry has exactly one type key, for example file:.",
      "A resource entry has exactly one type key, for example file:.",
    ]);
  });

  it("escapes pointer tokens", () => {
    const errs = check({ additionalProperties: { type: "string" } }, { "a/b~c": 1 });
    expect(errs.map((e) => e.pointer)).toEqual(["/a~1b~0c"]);
  });

  it("counts code points and checks integers and enums", () => {
    expect(check({ maxLength: 1 }, "😀")).toEqual([]);
    expect(check({ type: "integer" }, 1.5)).toHaveLength(1);
    expect(check({ type: "integer" }, 2)).toEqual([]);
    expect(check({ type: ["string", "null"] }, null)).toEqual([]);
    expect(check({ enum: ["a", 1] }, "b")).toHaveLength(1);
    expect(check(false, 1)).toHaveLength(1);
    expect(check({ minItems: 1 }, [])[0]?.message).toBe("Needs at least 1 entry.");
    expect(check({ maxItems: 1 }, [1, 2])[0]?.message).toBe("Allows at most 1 entry.");
    expect(check({ minProperties: 2 }, { a: 1 })[0]?.message).toBe("Needs at least 2 keys.");
  });

  it("compares objects deeply for uniqueItems and const", () => {
    expect(check({ uniqueItems: true }, [{ a: [1] }, { a: [1] }])[0]?.at).toBe("/1");
    expect(check({ uniqueItems: true }, [{ a: 1 }, { a: 2 }, { b: 1 }])).toEqual([]);
    expect(check({ const: { a: [1] } }, { a: [1] })).toEqual([]);
    expect(check({ const: { a: 1 } }, { a: 1, b: 2 })).toHaveLength(1);
  });
});

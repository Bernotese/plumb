// T-196 (D-376): the form writes only what the person set. A top-level
// Block parameter equal to its default is left out; a stored one stays.
import { describe, expect, it } from "vitest";
import { blockParams, fieldsOf, initialValues, pinnedDefaults, sameValue } from "./schemaForm";

const fields = fieldsOf({
  properties: {
    text: { type: "string", default: "Hello" },
    port: { type: "integer", default: 80 },
    tags: { type: "array", items: { type: "string" }, default: ["a"] },
    site: {
      type: "object",
      default: { tls: false, name: "x" },
      properties: { name: { type: "string" }, tls: { type: "boolean" } },
    },
    owner: { type: "string" },
  },
});

describe("Block parameters without defaults (D-376)", () => {
  it("leaves out every value equal to its default on a new use", () => {
    const v = initialValues(fields, {});
    expect(blockParams({}, fields, v)).toEqual({});
    v["owner"] = "me";
    v["port"] = "8080";
    expect(blockParams({}, fields, v)).toEqual({ owner: "me", port: 8080 });
  });

  it("keeps a stored value equal to the default, and names it as pinned", () => {
    const stored = { text: "Hello", port: 8080, site: { name: "x", tls: false } };
    const v = initialValues(fields, stored);
    expect(blockParams(stored, fields, v)).toEqual(stored);
    expect(pinnedDefaults(stored, fields, v)).toEqual(["text", "site"]);
  });

  it("follows the default again: the value is left out", () => {
    const stored = { text: "Hello" };
    const v = initialValues(fields, stored);
    expect(blockParams(stored, fields, v, ["text"])).toEqual({});
    expect(pinnedDefaults(stored, fields, v, ["text"])).toEqual([]);
    // A new value after "follow" is the person's value again.
    v["text"] = "Hi";
    expect(blockParams(stored, fields, v, ["text"])).toEqual({ text: "Hi" });
  });

  it("a stored value that the person changes to another value is written", () => {
    const stored = { port: 8080 };
    const v = initialValues(fields, stored);
    v["port"] = "80";
    // Stored keys stay: the person set this value on purpose.
    expect(blockParams(stored, fields, v)).toEqual({ port: 80 });
  });

  it("compares deeply and ignores key order", () => {
    expect(sameValue({ a: 1, b: [1, { c: 2 }] }, { b: [1, { c: 2 }], a: 1 })).toBe(true);
    expect(sameValue({ a: 1 }, { a: 1, b: undefined })).toBe(false);
    expect(sameValue([1, 2], [2, 1])).toBe(false);
    expect(sameValue("80", 80)).toBe(false);
  });
});

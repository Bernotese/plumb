import { describe, expect, it } from "vitest";
import { offersRaw, rawResources, withRaw } from "./raw";
import type { TargetSpec } from "./types";

const spec = {
  type: "vm",
  image: "debian-13",
  guest: {},
  blocks: [{ use: "motd" }],
  overrides: {
    "service:nginx": { enabled: false },
    "file:/etc/motd": { mode: "0640", content: "Hello\n" },
  },
} as unknown as TargetSpec;

describe("rawResources", () => {
  it("lists every override by id with its attributes and content", () => {
    expect(rawResources(spec)).toEqual([
      { id: "file:/etc/motd", attrs: [{ name: "mode", value: "0640" }], content: "Hello\n" },
      { id: "service:nginx", attrs: [{ name: "enabled", value: "false" }] },
    ]);
  });

  it("is empty without overrides", () => {
    expect(rawResources({ ...spec, overrides: undefined } as TargetSpec)).toEqual([]);
  });
});

describe("withRaw", () => {
  it("sets the content and keeps the other attributes and overrides", () => {
    const out = withRaw(spec, "file:/etc/motd", "Bye\n");
    expect(out.overrides).toEqual({
      "service:nginx": { enabled: false },
      "file:/etc/motd": { mode: "0640", content: "Bye\n" },
    });
    expect(spec.overrides?.["file:/etc/motd"]).toEqual({ mode: "0640", content: "Hello\n" });
  });

  it("removes the override for Back to the Block", () => {
    expect(withRaw(spec, "file:/etc/motd", null).overrides).toEqual({
      "service:nginx": { enabled: false },
    });
    expect(withRaw(spec, "file:/etc/motd", null).blocks).toEqual(spec.blocks);
  });
});

describe("offersRaw", () => {
  it("offers raw mode for agent resources only", () => {
    expect(offersRaw("file:/etc/motd")).toBe(true);
    expect(offersRaw("guest:config")).toBe(false);
    expect(offersRaw("guest:address")).toBe(false);
  });
});

describe("offersRaw for agent resources", () => {
  it("does not offer raw mode for agent:*", () => {
    expect(offersRaw("agent:silent")).toBe(false);
  });
});

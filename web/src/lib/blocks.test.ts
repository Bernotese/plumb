import { describe, expect, it } from "vitest";
import { docPointers, paramRows, resourceTypes, useErrors, usersOf } from "./blocks";
import type { BlockDoc, ImageView, TargetView } from "./types";

const doc = {
  kind: "Block",
  name: "motd",
  spec: {
    params: {
      type: "object",
      properties: {
        text: { type: "string", title: "Text", default: "Hi" },
        key: { type: "string", writeOnly: true },
        n: { type: ["integer", "null"] },
      },
      required: ["key"],
    },
    resources: [{ file: { path: "/etc/motd" } }, { package: { name: "x" } }, { file: {} }],
  },
} as unknown as BlockDoc;

describe("blocks helpers", () => {
  it("finds users", () => {
    const t = (name: string, uses: string[]) =>
      ({ document: { name, spec: { blocks: uses.map((use) => ({ use })) } } }) as TargetView;
    const i = (name: string, uses?: string[]) =>
      ({
        document: { name, spec: uses ? { build: { blocks: uses.map((use) => ({ use })) } } : {} },
      }) as ImageView;
    expect(
      usersOf(
        "motd",
        [t("b", ["motd"]), t("a", ["x", "motd"]), t("c", [])],
        [i("i", ["motd"]), i("s")],
      ),
    ).toEqual({ targets: ["a", "b"], images: ["i"] });
  });

  it("lists types and params", () => {
    expect(resourceTypes(doc)).toEqual(["file", "package"]);
    expect(paramRows(doc)).toEqual([
      {
        name: "text",
        type: "string",
        required: false,
        secret: false,
        default: '"Hi"',
        title: "Text",
      },
      { name: "key", type: "string", required: true, secret: true },
      { name: "n", type: "integer | null", required: false, secret: false },
    ]);
  });

  it("keeps the pointers of the first put document", () => {
    expect(
      docPointers({ "/put/0/spec": "a", "/put/0": "b", "/put/01": "c", "/reason": "d" }),
    ).toEqual({
      "/spec": "a",
      "": "b",
    });
  });

  it("maps render errors of the users to the Block resource", () => {
    const msg = (t: string) =>
      `Target "${t}": resource file:/etc/motd from /spec/blocks/0 (Block "motd" /spec/resources/0): attribute /mode: got number, want string`;
    const errors = [
      { pointer: "/spec/blocks", message: msg("web1") },
      { pointer: "/spec/blocks", message: msg("web2") },
      {
        pointer: "/spec/blocks/1",
        message: 'Target "web1": Block "motd" /spec/resources/2/file/content: template: bad',
      },
      {
        pointer: "/spec/build/blocks",
        message:
          'Image "img": resource file:x from /spec/build/blocks/0 (Block "motd" /spec/resources/2): attribute : missing property \'path\'',
      },
      { pointer: "/spec/blocks/0", message: 'Target "web1": Block "motdx" /spec/resources/0: no' },
      { pointer: "/put/0/spec", message: 'Block "motd" /spec/resources/0: at the put' },
      { pointer: "/spec/x", message: "other" },
    ];
    const { found, handled } = useErrors(errors, "motd", doc);
    expect(found).toEqual([
      {
        pointer: "/spec/resources/0/file/mode",
        message: "mode: got number, want string",
        docs: ["Target web1", "Target web2"],
      },
      {
        pointer: "/spec/resources/2/file/content",
        message: "template: bad",
        docs: ["Target web1"],
      },
      {
        pointer: "/spec/resources/2/file",
        message: "missing property 'path'",
        docs: ["Image img"],
      },
    ]);
    expect(handled).toEqual(errors.slice(0, 4));
  });
});

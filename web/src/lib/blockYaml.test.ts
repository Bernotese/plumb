import { describe, expect, it } from "vitest";
import { MAX_TEXT, parseBlock, tokens } from "./blockYaml";

const ok = `kind: Block
name: motd
spec:
  title: Message of the day
  params:
    type: object
    properties:
      text: {type: string}
  resources:
    - file:
        path: /etc/motd
        content: |
          {{ .text }}
        mode: "0644"
`;

describe("parseBlock", () => {
  it("parses a valid Block", () => {
    const p = parseBlock(ok);
    expect(p.errors).toEqual([]);
    expect(p.value).toMatchObject({
      kind: "Block",
      name: "motd",
      spec: {
        resources: [{ file: { path: "/etc/motd", content: "{{ .text }}\n", mode: "0644" } }],
      },
    });
  });

  it("finds the line of a pointer", () => {
    const p = parseBlock(ok);
    expect(p.lineOf("")).toBe(1);
    expect(p.lineOf("/name")).toBe(2);
    expect(p.lineOf("/spec/params/properties/text")).toBe(8);
    expect(p.lineOf("/spec/resources/0")).toBe(10);
    expect(p.lineOf("/spec/resources/0/file/mode")).toBe(14);
    // A missing part ends at the deepest part that exists.
    expect(p.lineOf("/spec/resources/0/file/owner")).toBe(10);
    expect(p.lineOf("/spec/resources/7")).toBe(9);
    expect(p.lineOf("/spec/after")).toBe(3);
  });

  it("shows schema errors at their line", () => {
    const p = parseBlock(ok.replace("  title:", "  titel:").replace('mode: "0644"', "mode: 0644"));
    expect(p.errors).toEqual([
      expect.objectContaining({
        line: 4,
        pointer: "/spec",
        message: expect.stringContaining("titel"),
      }),
    ]);
    // 0644 is the number 644 in YAML 1.2; the resource schema on the server refuses it.
    expect(p.value).toMatchObject({ spec: { resources: [{ file: { mode: 644 } }] } });
  });

  it("reports YAML errors with a line and no value", () => {
    const p = parseBlock("kind: Block\nname: a\nspec:\n  resources: [\n");
    expect(p.value).toBeUndefined();
    expect(p.errors.length).toBeGreaterThan(0);
    expect(p.errors[0]?.line).toBeGreaterThan(0);
    expect(p.errors[0]?.message).not.toContain("\n");
  });

  it("refuses duplicate keys, aliases, anchors, tags, complex keys and a second document", () => {
    const base = "kind: Block\nname: a\nspec:\n  resources: [{file: {}}]\n";
    const bad = {
      duplicate: base + "name: b\n",
      alias: "kind: Block\nname: &n a\nspec:\n  title: *n\n  resources: [{file: {}}]\n",
      tag: base.replace("name: a", "name: !custom a"),
      complexKey: base + "? [a]\n: 1\n",
      boolKey: base.replace("kind: Block", "kind: Block\ntrue: 1"),
      twoDocs: base + "---\n" + base,
    };
    for (const [what, text] of Object.entries(bad)) {
      const p = parseBlock(text);
      expect(p.value, what).toBeUndefined();
      expect(p.errors.length, what).toBeGreaterThan(0);
      expect(
        p.errors.every((e) => e.line > 0),
        what,
      ).toBe(true);
    }
    expect(parseBlock(bad.alias).errors.map((e) => e.line)).toEqual([2, 4]);
  });

  it("keeps YAML 1.1 words as strings", () => {
    const p = parseBlock(
      "kind: Block\nname: a\nspec:\n  title: yes\n  description: off\n  resources: [{file: {}}]\n",
    );
    expect(p.errors).toEqual([]);
    expect(p.value).toMatchObject({ spec: { title: "yes", description: "off" } });
  });

  it("refuses empty and too long text", () => {
    expect(parseBlock("").errors[0]?.message).toBe("The text is empty.");
    expect(parseBlock("# only a comment\n").errors[0]?.message).toBe("The text is empty.");
    expect(parseBlock("x".repeat(MAX_TEXT + 1)).errors[0]?.message).toMatch(/longer than/);
  });

  it("reports a non-object document at line 1", () => {
    const p = parseBlock("just text\n");
    expect(p.errors[0]).toMatchObject({ line: 1, pointer: "" });
  });

  it("splits pointers", () => {
    expect(tokens("")).toEqual([]);
    expect(tokens("/a~1b/~0c/0")).toEqual(["a/b", "~c", "0"]);
  });
});

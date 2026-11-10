import { describe, expect, it } from "vitest";
import { scalar, toYaml } from "./yaml";

describe("scalar", () => {
  it("keeps safe strings plain", () => {
    expect(scalar("ubuntu-26.04")).toBe("ubuntu-26.04");
    expect(scalar("Message of the day.")).toBe("Message of the day.");
    expect(scalar("/etc/motd")).toBe("/etc/motd");
  });
  it("quotes strings YAML would read as something else", () => {
    for (const s of [
      "",
      "true",
      "No",
      "null",
      "~",
      "0644",
      "1.5",
      "-3",
      "a: b",
      "#x",
      "x #y",
      "{{ .text }}",
      "a ",
      " a",
      "*a",
      "- a",
      "y",
    ]) {
      expect(scalar(s)).toBe(JSON.stringify(s));
    }
  });
});

describe("toYaml", () => {
  it("writes a document in API key order", () => {
    const doc = {
      kind: "Target",
      name: "web1",
      spec: {
        type: "vm",
        guest: { cores: 2, ip: "dhcp" },
        blocks: [{ use: "motd", params: { text: "Hello from Plumb" } }, { use: "nginx" }],
        overrides: {},
        tags: [],
        list: ["a", 1, true, null],
        nested: [["x", "z"]],
      },
    };
    expect(toYaml(doc)).toBe(
      [
        "kind: Target",
        "name: web1",
        "spec:",
        "  type: vm",
        "  guest:",
        "    cores: 2",
        "    ip: dhcp",
        "  blocks:",
        "    - use: motd",
        "      params:",
        "        text: Hello from Plumb",
        "    - use: nginx",
        "  overrides: {}",
        "  tags: []",
        "  list:",
        "    - a",
        "    - 1",
        "    - true",
        "    - null",
        "  nested:",
        "    - - x",
        "      - z",
        "",
      ].join("\n"),
    );
  });
  it("writes multi-line strings as literal blocks", () => {
    expect(toYaml({ content: "a\n\nb\n" })).toBe("content: |\n  a\n\n  b\n");
    expect(toYaml({ content: "a\nb" })).toBe("content: |-\n  a\n  b\n");
    expect(toYaml({ content: "a\n\n" })).toBe("content: |+\n  a\n\n");
    expect(toYaml(["x\ny\n"])).toBe("- |\n  x\n  y\n");
  });
  it("quotes multi-line strings that a literal block cannot hold", () => {
    expect(toYaml({ c: " lead\nx" })).toBe('c: " lead\\nx"\n');
    expect(toYaml({ c: "a\r\nb" })).toBe('c: "a\\r\\nb"\n');
  });
  it("writes scalars at the top", () => {
    expect(toYaml(null)).toBe("null\n");
    expect(toYaml({})).toBe("{}\n");
  });
});

describe("YAML QA escaping", () => {
  it.each([
    'say "hello"',
    "it's fine",
    "a: b # comment",
    "\\tmp\\file",
    "tab\there",
    "nul\0end",
    "😀 <>&",
    "---",
    "[a, b]",
  ])("preserves %j in keys and values", (value) => {
    expect(toYaml({ [value]: value })).toBe(`${JSON.stringify(value)}: ${JSON.stringify(value)}\n`);
  });

  it.each(["\n", "\n\n", " \n\t", "\tfirst\nnext"])(
    "quotes whitespace-only or indented content %j",
    (value) => {
      expect(toYaml({ content: value })).toBe(`content: ${JSON.stringify(value)}\n`);
    },
  );

  it("preserves nested multiline text and trailing newlines", () => {
    expect(toYaml({ items: [{ content: "a\n  b\n\n" }] })).toBe(
      "items:\n  - content: |+\n      a\n        b\n\n",
    );
  });
});

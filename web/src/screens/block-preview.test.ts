// @vitest-environment happy-dom
// T-239 (D-426): the Block editor preview. The parameter form a Target gets
// and the resource list, updated while the person types. It is a local
// approximation; the server renders only when a plan is made.
import { afterEach, describe, expect, it } from "vitest";
import { typeNames } from "../lib/resourceTypes";
import { createMockApi } from "../lib/mock/mock";
import { fieldsOf } from "../lib/schemaForm";
import BlockForm, {
  fillTemplate,
  keyAttr,
  previewResources,
  withDefaults,
} from "./BlockForm.svelte";
import { button, el, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const textOf = () => document.querySelector<HTMLTextAreaElement>("#b-text")?.value ?? "";
const resources = () =>
  [...document.querySelectorAll("#b-resources li")].map((l) =>
    (l.textContent ?? "").replace(/\s+/g, " ").trim(),
  );
const preview = () => text(el("aside.preview"));

describe("block preview helpers", () => {
  it("names the key attribute of every resource type as internal/resource does", () => {
    const path = typeNames().filter((t) => keyAttr(t) === "path");
    expect(path.sort()).toEqual(["directory", "file", "symlink"]);
    for (const t of typeNames()) if (!path.includes(t)) expect(keyAttr(t), t).toBe("name");
  });

  it("fills simple actions with scalar parameters and trims like Go", () => {
    const fields = fieldsOf({
      properties: {
        a: { type: "string" },
        n: { type: "integer" },
        b: { type: "boolean" },
        o: { type: "object", properties: { k: { type: "string" } } },
        x: { type: "number" },
      },
    });
    const p = { a: "x", n: 3, b: false, o: { k: "deep" }, x: NaN };
    expect(fillTemplate("/{{ .a }}/{{.n}}/{{ .b }}/{{ .o.k }}", p, fields)).toEqual({
      text: "/x/3/false/deep",
      template: false,
    });
    // L1: "{{- " and " -}}" trim the whitespace next to the action, as in Go.
    expect(fillTemplate("a {{- .a }} b", p, fields).text).toBe("ax b");
    expect(fillTemplate("a {{ .a -}} b", p, fields).text).toBe("a xb");
    expect(fillTemplate("a\n {{- .a -}} \tb", p, fields).text).toBe("axb");
    // Row 38 of the T-260 log: a trim mark trims the text after the action
    // before it, as Go does ("{{ .a }} {{- .b }}" is "xy", not "x y").
    expect(fillTemplate("{{ .a }} {{- .n }}", p, fields).text).toBe("x3");
    expect(fillTemplate("{{ .a -}} {{ .n }}", p, fields).text).toBe("x3");
    expect(fillTemplate("{{ .a -}}  {{- .n }} .", p, fields).text).toBe("x3 .");
    // Around an action that only the server fills, the trim marks still
    // trim (Go does), and the action stays as written.
    expect(fillTemplate("a {{- .a | upper }} b", p, fields)).toEqual({
      text: "a{{- .a | upper }} b",
      template: true,
    });
    // Only the server fills an object, a NaN, a pipe or a function.
    for (const t of ["{{ .o }}", "{{ .x }}", "{{ .a | upper }}", "{{ printf .a }}"])
      expect(fillTemplate(t, p, fields), t).toEqual({ text: t, template: true });
  });

  it("marks what the server refuses: unknown parameter, missing value, secret", () => {
    const fields = fieldsOf({
      properties: {
        a: { type: "string" },
        opt: { type: "string" },
        o: { type: "object", properties: { k: { type: "string" } } },
        s: { type: "string", writeOnly: true },
      },
    });
    const p = { a: "x", o: {}, s: { secret: "db" } };
    const refused = (t: string) => fillTemplate(t, p, fields).refused;
    expect(refused("{{ .nope }}")).toBe(".nope is not a parameter");
    expect(refused("{{ .nope | lower }}")).toBe(".nope is not a parameter");
    expect(refused("{{ .opt }}")).toBe("no value for .opt"); // missingkey=error
    expect(refused("{{ .o.k }}")).toBe("no value for .o.k");
    // M1: a secret never fills a key, not even its name.
    for (const t of ["{{ .s }}", "{{ .s.secret }}", "{{ .s | lower }}"]) {
      expect(refused(t), t).toBe("a key cannot hold a secret");
      expect(fillTemplate(t, p, fields).text).not.toContain("db");
    }
    expect(refused("{{ .a }}")).toBeUndefined();
    // Row 39 of the T-260 log: ".x" inside a string literal is text, not a
    // parameter reference (Go strings, raw strings and runes).
    for (const t of ['{{ printf "%s .nope" .a }}', "{{ printf `.nope` .a }}", "{{ printf '.' }}"])
      expect(refused(t), t).toBeUndefined();
    expect(refused('{{ printf "\\" .nope }}'), "an escaped quote ends no literal").toBe(
      ".nope is not a parameter",
    );
    expect(refused('{{ printf ".a" .nope }}')).toBe(".nope is not a parameter");
  });

  it("fills a missing value with the parameter default, as the server does", () => {
    const fields = fieldsOf({
      properties: { site: { type: "string", default: "shop" } },
    });
    expect(withDefaults({}, fields)).toEqual({ site: "shop" });
    expect(previewResources([{ file: { path: "/etc/{{ .site }}" } }], {}, fields)).toEqual([
      { type: "file", key: "/etc/shop", template: false },
    ]);
  });

  it("lists resources in order with their marks", () => {
    const fields = fieldsOf({ properties: { site: { type: "string" } } });
    const r = previewResources(
      [
        { package: { name: "nginx" } },
        { file: { path: "/etc/{{ .site }}.conf" } },
        { file: { path: "/etc/{{ .site | lower }}" } },
        { service: {} },
        { sysctl: { name: "vm.swappiness" } },
        { file: { path: "/etc/shop.conf" } },
        { file: { path: "{{ .site | lower }}" } },
        { file: { path: "/etc/{{ .site | lower }}" } },
        { package: { name: "" } },
        { tarball: { name: "x" } },
        { a: {}, b: {} },
        "text",
        null,
      ],
      { site: "shop" },
      fields,
    );
    expect(r).toEqual([
      { type: "package", key: "nginx", template: false },
      { type: "file", key: "/etc/shop.conf", template: false },
      { type: "file", key: "/etc/{{ .site | lower }}", template: true },
      { type: "service", key: "", template: false, refused: "no name" },
      { type: "sysctl", key: "vm.swappiness", template: false, only: "vm" },
      // L3: the server refuses a second resource with the same id.
      {
        type: "file",
        key: "/etc/shop.conf",
        template: false,
        refused: "the same id as resource 2",
      },
      { type: "file", key: "{{ .site | lower }}", template: true },
      // Two template keys: only the server knows whether they are equal.
      { type: "file", key: "/etc/{{ .site | lower }}", template: true },
      { type: "package", key: "", template: false, refused: "empty name" },
      { type: "tarball", key: "x", template: false, refused: "unknown resource type tarball" },
    ]);
  });
});

describe("block editor preview", () => {
  it("shows the parameter form and the resources of a stored Block", async () => {
    show(BlockForm, { api: adminApi(), name: "app" });
    await until(() => textOf().includes("name: app"));
    expect(preview()).toContain("local approximation");
    expect(el<HTMLInputElement>("#preview-db_host").value).toBe("db1");
    expect(resources()).toEqual(["file:/etc/app/db.conf"]);
    // No field error before the person types in the preview (db_password is required).
    expect(document.querySelector("aside.preview .error")).toBeNull();
  });

  it("updates the resource keys from the YAML and the preview values", async () => {
    show(BlockForm, { api: adminApi(), name: "app" });
    await until(() => textOf().includes("name: app"));
    type(
      "#b-text",
      textOf().replace("path: /etc/app/db.conf", "path: /etc/{{ .db_host }}/db.conf"),
    );
    await until(() => resources()[0] === "file:/etc/db1/db.conf");
    type("#preview-db_host", "db7");
    expect(resources()).toEqual(["file:/etc/db7/db.conf"]);
    // A preview value never changes the Block text.
    expect(textOf()).toContain("path: /etc/{{ .db_host }}/db.conf");
    expect(textOf()).not.toContain("db7");
  });

  it("has no secret picker: it lists no secrets and makes none", async () => {
    const api = adminApi();
    let secretCalls = 0;
    const counted = new Proxy(api, {
      get(t, k, r) {
        if (k === "listSecrets" || k === "putSecret") secretCalls++;
        return Reflect.get(t, k, r);
      },
    });
    show(BlockForm, { api: counted, name: "app" });
    await until(() => textOf().includes("name: app"));
    type("#b-text", textOf().replace("path: /etc/app/db.conf", "path: /etc/{{ .db_password }}"));
    await until(() => resources()[0]?.startsWith("file:/etc/{{"));
    expect(
      document.querySelector("aside.preview select, aside.preview input[type=password]"),
    ).toBeNull();
    type("#preview-db_host", "db2");
    expect(preview()).not.toContain("Choose a secret.");
    expect(button("New secret")).toBeUndefined();
    expect(resources()).toEqual([
      "file:/etc/{{ .db_password }} (refused: a key cannot hold a secret)",
    ]);
    expect(secretCalls).toBe(0);
  });

  it("marks a VM-only resource type", async () => {
    show(BlockForm, { api: adminApi(), name: "net-tuning" });
    await until(() => textOf().includes("name: net-tuning"));
    const r = await until(() => resources().length > 0 && resources());
    expect(r.every((x) => x.startsWith("sysctl:") && x.endsWith("(vm only)"))).toBe(true);
  });

  it("follows a changed parameter schema and keeps values by name", async () => {
    show(BlockForm, { api: adminApi(), name: "app" });
    await until(() => textOf().includes("name: app"));
    type("#preview-db_host", "db9");
    type(
      "#b-text",
      textOf().replace(
        "properties:\n",
        "properties:\n      port: { type: integer, title: Port, default: 5432 }\n",
      ),
    );
    await until(() => document.querySelector("#preview-port"));
    expect(el<HTMLInputElement>("#preview-port").value).toBe("5432");
    expect(el<HTMLInputElement>("#preview-db_host").value).toBe("db9");
    type("#preview-port", "1.5");
    await until(() => preview().includes("Enter a whole number."));
  });

  it("says what is missing for a new Block, broken text and bad params", async () => {
    show(BlockForm, { api: adminApi() });
    await until(() => textOf().startsWith("kind: Block"));
    expect(document.querySelector("#b-params-none, #b-params")).not.toBeNull();
    type("#b-text", "kind: [");
    await until(() => document.querySelector("#b-preview-none"));
    type("#b-text", "kind: Block\nname: x\nspec:\n  params: 3\n  resources: []\n");
    await until(() => document.querySelector("#b-params-bad"));
    expect(document.querySelector("#b-resources-none")).not.toBeNull();
  });
});

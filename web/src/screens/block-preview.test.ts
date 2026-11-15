// @vitest-environment happy-dom
// T-239 (D-426): the Block editor preview. The parameter form a Target gets
// and the resource list, updated while the person types. It is a local
// approximation; the server renders only when a plan is made.
import { afterEach, describe, expect, it } from "vitest";
import resourceKinds from "../lib/resourceKinds.json";
import { createMockApi } from "../lib/mock/mock";
import BlockForm, { fillTemplate, keyAttr, previewResources } from "./BlockForm.svelte";
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
    const path = Object.keys(resourceKinds).filter((t) => keyAttr(t) === "path");
    expect(path.sort()).toEqual(["directory", "file", "symlink"]);
    for (const t of Object.keys(resourceKinds))
      if (!path.includes(t)) expect(keyAttr(t), t).toBe("name");
  });

  it("fills only simple actions with scalar parameters", () => {
    const p = { a: "x", n: 3, b: false, o: { k: "deep" }, s: { secret: "db" }, nan: NaN };
    expect(fillTemplate("/{{ .a }}/{{.n}}/{{- .b -}}/{{ .o.k }}", p)).toBe("/x/3/false/deep");
    // A secret, an object, a missing key, a pipe and a function stay as written.
    for (const t of ["{{ .s }}", "{{ .o }}", "{{ .nope }}", "{{ .a | upper }}", "{{ printf .a }}"])
      expect(fillTemplate(t, p)).toBe(t);
    expect(fillTemplate("{{ .nan }}", p)).toBe("{{ .nan }}");
    expect(fillTemplate("{{ .s.secret }}", p)).toBe("db"); // a secret *name*, never its value
  });

  it("lists resources in order and skips entries the schema refuses", () => {
    const r = previewResources(
      [
        { package: { name: "nginx" } },
        { file: { path: "/etc/{{ .site }}.conf" } },
        { file: { path: "/etc/{{ .site | lower }}" } },
        { service: {} },
        { a: {}, b: {} },
        "text",
        null,
      ],
      { site: "shop" },
    );
    expect(r).toEqual([
      { type: "package", key: "nginx", template: false },
      { type: "file", key: "/etc/shop.conf", template: false },
      { type: "file", key: "/etc/{{ .site | lower }}", template: true },
      { type: "service", key: "", template: false },
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
    expect(resources()).toEqual(["file:/etc/{{ .db_password }}(template)"]);
    expect(secretCalls).toBe(0);
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

// @vitest-environment happy-dom
// Screen tests of T-096: Blocks list, Block page, Block editor (YAML, D-261).
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../lib/api";
import { usersOf } from "../lib/blocks";
import { parseBlock } from "../lib/blockYaml";
import { createMockApi } from "../lib/mock/mock";
import { toYaml } from "../lib/yaml";
import BlockForm from "./BlockForm.svelte";
import Blocks from "./Blocks.svelte";
import BlockScreen from "./BlockView.svelte";
import { button, el, planOpened, reset, rows, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const editor = () => el<HTMLTextAreaElement>("#b-text");
const textOf = () => document.querySelector<HTMLTextAreaElement>("#b-text")?.value ?? "";
const errorItems = () =>
  [...document.querySelectorAll("#b-errors li")].map((l) => l.textContent?.replace(/\s+/g, " "));

describe("blocks list", () => {
  it("lists every Block with its resources and users", async () => {
    const api = adminApi();
    show(Blocks, { api });
    const blocks = await api.listBlocks();
    const r = await until(() => rows().length === blocks.length && rows());
    const names = blocks.map((b) => b.document.name).sort();
    expect(r.map((x) => x.split(" ")[0])).toEqual(names);
    const motd = r.find((x) => x.startsWith("motd"));
    expect(motd).toMatch(/1 resource ?file/);
    const u = usersOf("motd", await api.listTargets(), await api.listImages());
    expect(u.targets.length).toBeGreaterThan(0);
    expect(motd).toContain(`${u.targets.length} target`);
    expect(el<HTMLAnchorElement>("a.btn.primary").getAttribute("href")).toBe("#/blocks/new");
  });
});

describe("block page", () => {
  it("shows parameters, users and the YAML text", async () => {
    const api = adminApi();
    show(BlockScreen, { api, name: "motd" });
    await until(() => text().includes("Used by") && text().includes("Parameters"));
    const u = usersOf("motd", await api.listTargets(), await api.listImages());
    for (const t of u.targets) expect(text()).toContain(t);
    expect(rows()[0]).toMatch(/^text Message ?string ?no ?"Managed by Plumb."$/);
    const yaml = [...document.querySelectorAll(".code li")].map((l) => l.textContent);
    expect(yaml[0]).toBe("kind: Block");
    expect(yaml[1]).toBe("name: motd");
    expect(el<HTMLAnchorElement>("a.btn.primary").getAttribute("href")).toBe("#/blocks/motd/edit");
  });

  it("shows a missing Block as an error", async () => {
    show(BlockScreen, { api: adminApi(), name: "nope" });
    await until(() => document.querySelector('[role="alert"]'));
    expect(text()).toContain("does not exist");
  });
});

describe("block editor", () => {
  it("round-trips every mock Block through the YAML text", async () => {
    for (const b of await adminApi().listBlocks()) {
      const p = parseBlock(toYaml(b.document));
      expect(p.errors, b.document.name).toEqual([]);
      expect(p.value).toEqual(b.document);
    }
  });

  it("makes a new Block from the template after a reason", async () => {
    const api = adminApi();
    show(BlockForm, { api });
    await until(() => textOf().startsWith("kind: Block"));
    type("#b-text", editor().value.replace("name: my-block", "name: banner"));
    button("Show the plan")?.click();
    await until(() => document.querySelector("#b-reason-error"));
    expect(el("#b-reason").getAttribute("aria-invalid")).toBe("true");
    expect(text()).toContain("The text matches the Block schema.");
    type("#b-reason", "Add a banner block.");
    button("Show the plan")?.click();
    const id = await planOpened();
    const plan = await api.getPlan(id);
    // No Target uses the new Block: the Plan touches none.
    expect(plan.targets).toEqual([]);
    expect(plan.reason).toBe("Add a banner block.");
  });

  it("shows schema and YAML errors at their line and does not save", async () => {
    const api = adminApi();
    let calls = 0;
    show(BlockForm, {
      api: { ...api, createPlan: (p: never) => (calls++, api.createPlan(p)) },
    });
    await until(() => textOf().startsWith("kind: Block"));
    type("#b-text", editor().value.replace("  title:", "  titel:"));
    // Errors show after the first blur, then live.
    expect(errorItems()).toEqual([]);
    editor().dispatchEvent(new Event("blur"));
    await until(() => errorItems().length === 1);
    expect(errorItems()[0]).toMatch(/^Line 4: Unknown key "titel"/);
    expect(editor().getAttribute("aria-invalid")).toBe("true");
    expect(editor().getAttribute("aria-describedby")).toContain("b-errors");
    expect(el(".gutter .bad").textContent).toBe("!4");
    // The line button moves the caret to the line.
    button("Line 4")?.click();
    expect(document.activeElement).toBe(editor());
    expect(editor().value.slice(editor().selectionStart, editor().selectionEnd)).toBe(
      "  titel: My block",
    );
    type("#b-text", editor().value + "  bad: [\n");
    await until(() =>
      errorItems().some((e) => /^Line \d+:/.test(e ?? "") && !e?.includes("titel")),
    );
    type("#b-reason", "Try.");
    button("Show the plan")?.click();
    await until(() => document.activeElement?.id === "b-errors");
    expect(calls).toBe(0);
  });

  it("refuses a duplicate name for a new Block", async () => {
    show(BlockForm, { api: adminApi() });
    await until(() => textOf().startsWith("kind: Block"));
    type("#b-text", editor().value.replace("name: my-block", "name: motd"));
    editor().dispatchEvent(new Event("blur"));
    await until(() => errorItems().length === 1);
    expect(errorItems()[0]).toBe('Line 2: A Block "motd" exists already.');
  });

  it("edits a Block: the name stays, the Plan lists every Target that uses it", async () => {
    const api = adminApi();
    const users = usersOf("motd", await api.listTargets(), await api.listImages());
    show(BlockForm, { api, name: "motd" });
    await until(() => textOf().includes("name: motd"));
    expect(text()).toContain(`Saving plans ${users.targets.length} Target`);
    // Submit stays enabled; a save without a change says so.
    button("Show the plan")?.click();
    await until(() => text().includes("The text has no change."));

    type("#b-text", editor().value.replace("name: motd", "name: motd2"));
    editor().dispatchEvent(new Event("blur"));
    await until(() => errorItems().length === 1);
    expect(errorItems()[0]).toMatch(/^Line 2: The name must stay "motd"/);

    const doc = (await api.getBlock("motd")).document;
    const resources = doc.spec.resources.map((r) =>
      "file" in r ? { file: { ...r["file"], mode: "0600" } } : r,
    );
    type("#b-text", toYaml({ ...doc, spec: { ...doc.spec, resources } }));
    await until(() => errorItems().length === 0);
    type("#b-reason", "Tighter motd mode.");
    button("Show the plan")?.click();
    const plan = await api.getPlan(await planOpened());
    expect(plan.targets.map((t) => t.target).sort()).toEqual(users.targets);
    for (const t of plan.targets) {
      const r = t.resources.find((x) => x.resource === "file:/etc/motd");
      expect(r?.changes).toEqual([
        expect.objectContaining({ attr: "mode", from: "0644", to: "0600" }),
      ]);
    }
  });

  it("shows a 422 of the server at its line", async () => {
    const api = adminApi();
    const fail = () =>
      Promise.reject(
        new ApiError({
          type: "urn:plumb:problem:validation",
          title: "Validation failed",
          status: 422,
          detail: "The documents are not valid.",
          errors: [
            { pointer: "/put/0/spec/resources/0/file/mode", message: 'Block "motd": bad mode' },
            { pointer: "/spec/blocks/0", message: 'Target "web1": other document' },
          ],
        }),
      );
    show(BlockForm, { api: { ...api, createPlan: fail }, name: "motd" });
    await until(() => textOf().includes("name: motd"));
    type("#b-text", editor().value.replace("Message of the day.", "MOTD."));
    type("#b-reason", "Try.");
    button("Show the plan")?.click();
    await until(() => errorItems().length === 1);
    const line =
      editor()
        .value.split("\n")
        .findIndex((l) => l.includes("mode:")) + 1;
    expect(errorItems()[0]).toBe(`Line ${line}: Block "motd": bad mode (server)`);
    // The pointer into another document stays in the problem box.
    expect(el('[role="alert"]').textContent).toContain("/spec/blocks/0");
    expect(el('[role="alert"]').textContent).not.toContain("/put/0/spec/resources");
  });
});

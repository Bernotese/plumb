// @vitest-environment happy-dom
// T-177 (D-363): the YAML mode of the Target and Image forms. It shows the
// document that the form would save, and a YAML edit is saved by the same
// proposal (createPlan) as the form; the server checks it.
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { ImageSpec, TargetDoc } from "../lib/types";
import { toYaml } from "../lib/yaml";
import ImageForm from "./ImageForm.svelte";
import TargetForm from "./TargetForm.svelte";
import { button, el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const submit = () => {
  el<HTMLFormElement>("form").requestSubmit();
  flushSync();
};
const yaml = () => el<HTMLTextAreaElement>("#t-yaml").value;

describe("Target YAML mode", () => {
  it("shows what the form saves; form and YAML make the same proposal", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    const stored = (await api.getTarget("web1")).document;
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#guest-cores"));
    // The form, unchanged.
    type("#t-reason", "Same.");
    submit();
    await planOpened();
    const fromForm = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    // The guest keeps its stored values and key order.
    expect(toYaml(fromForm.spec.guest)).toBe(toYaml(stored.spec.guest));
    reset();
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#guest-cores"));
    button("YAML")?.click();
    await until(() => document.querySelector("#t-yaml"));
    expect(button("YAML")?.getAttribute("aria-pressed")).toBe("true");
    expect(yaml()).toBe(toYaml(fromForm));
    // YAML → form → YAML gives the same text.
    button("Form")?.click();
    await until(() => document.querySelector("#guest-cores"));
    expect(el<HTMLInputElement>("#guest-cores").value).toBe(String(stored.spec.guest.cores));
    button("YAML")?.click();
    await until(() => document.querySelector("#t-yaml"));
    expect(yaml()).toBe(toYaml(fromForm));
    type("#t-reason", "Same.");
    submit();
    await until(() => createPlan.mock.calls.length === 2);
    expect(createPlan.mock.calls[1]?.[0].put?.[0]).toEqual(fromForm);
  });

  it("saves a YAML edit as typed", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#guest-cores"));
    button("YAML")?.click();
    await until(() => document.querySelector("#t-yaml"));
    const before = yaml();
    const memory = Number(/memory: (\d+)/.exec(before)?.[1]);
    const edited = before.replace(`memory: ${memory}`, `memory: ${memory * 2}`);
    type("#t-yaml", edited);
    type("#t-reason", "More memory.");
    submit();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(toYaml(doc)).toBe(edited);
    expect(doc.spec.guest.memory).toBe(memory * 2);
  });

  it("an edit in YAML shows in the form", async () => {
    const api = adminApi();
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#guest-cores"));
    button("YAML")?.click();
    await until(() => document.querySelector("#t-yaml"));
    type("#t-yaml", yaml().replace(/cores: \d+/, "cores: 7"));
    button("Form")?.click();
    await until(() => document.querySelector("#guest-cores"));
    expect(el<HTMLInputElement>("#guest-cores").value).toBe("7");
  });

  it("YAML problems stay in YAML, with their line; no plan", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#guest-cores"));
    button("YAML")?.click();
    await until(() => document.querySelector("#t-yaml"));
    type(
      "#t-yaml",
      yaml().replace("name: web1", "name: web9").replace("spec:", "spec:\n  x: &a 1"),
    );
    button("Form")?.click();
    flushSync();
    expect(document.querySelector("#t-yaml")).not.toBeNull();
    expect(text()).toContain("Fix them to switch back to the form.");
    expect(text()).toMatch(/Line \d+: Anchors \(&name\) are not allowed\./);
    type("#t-yaml", yaml().replace("  x: &a 1\n", ""));
    type("#t-reason", "Rename.");
    submit();
    await until(() => text().includes('The name must stay "web1".'));
    expect(text()).toMatch(/Line 2: The name must stay "web1"\./);
    expect(createPlan).not.toHaveBeenCalled();
  });

  it("an invalid form does not switch to YAML", async () => {
    const api = adminApi();
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#guest-cores"));
    type("#guest-cores", "many");
    button("YAML")?.click();
    flushSync();
    expect(document.querySelector("#t-yaml")).toBeNull();
    expect(el("#t-mode-error").textContent).toContain("Fix the marked fields first.");
  });

  it("a new Target takes its name from the YAML text", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api });
    await until(() => document.querySelector("#guest-cores"));
    type("#t-name", "new1");
    button("YAML")?.click();
    await until(() => document.querySelector("#t-yaml"));
    type("#t-yaml", yaml().replace("name: new1", "name: new2"));
    type("#t-reason", "New.");
    submit();
    await planOpened();
    expect(createPlan.mock.calls[0]?.[0].put?.[0]?.name).toBe("new2");
  });
});

describe("Image YAML mode", () => {
  it("shows the stored Image and saves a YAML edit", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    const stored = (await api.getImage("ubuntu-26.04")).document;
    show(ImageForm, { api, name: "ubuntu-26.04" });
    await until(() => document.querySelector("#i-url"));
    button("YAML")?.click();
    await until(() => document.querySelector("#i-yaml"));
    const text0 = el<HTMLTextAreaElement>("#i-yaml").value;
    expect(text0).toBe(toYaml(stored));
    type("#i-yaml", text0.replace("spec:\n", "spec:\n  title: Ubuntu from YAML\n"));
    type("#i-reason", "New title.");
    submit();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as { spec: ImageSpec };
    expect(doc.spec).toEqual({ ...stored.spec, title: "Ubuntu from YAML" });
  });
});

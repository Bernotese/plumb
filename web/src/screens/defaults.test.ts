// @vitest-environment happy-dom
// T-196 (D-376): the Target form writes only what the person set. A Block
// default change reaches a Target that never chose a value; a value stored
// equal to the default ("fixed to the current default") stays until the
// person chooses "Follow the Block default".
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { BlockDoc, TargetDoc } from "../lib/types";
import ImageForm from "./ImageForm.svelte";
import TargetForm from "./TargetForm.svelte";
import { button, el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const submit = () => {
  el<HTMLFormElement>("form").requestSubmit();
  flushSync();
};

/** A plan that changes the default of the motd parameter `text`. */
async function newMotdDefault(api: ReturnType<typeof adminApi>, value: string) {
  const motd = (await api.getBlock("motd")).document as BlockDoc;
  const next = structuredClone(motd);
  const props = next.spec.params?.properties as Record<string, { default?: unknown }>;
  expect(props["text"]?.default).toBeDefined();
  props["text"] = { ...props["text"], default: value };
  return api.createPlan({ reason: "New motd default.", put: [next] });
}

const targetsOf = (p: { targets: { target: string }[] }) => p.targets.map((t) => t.target);

describe("form writes only what the person set (D-376)", () => {
  it("a new use writes no default and no empty overrides; a new default reaches it", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api });
    await until(() => document.querySelector("#add-choice"));
    type("#t-name", "new1");
    type("#add-choice", "motd");
    button("Add Block")?.click();
    await until(() => document.querySelector('[id^="use-"][id$="-text"]'));
    type("#t-reason", "New.");
    submit();
    const id = await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.spec.blocks).toEqual([{ use: "motd" }]);
    expect(doc.spec).not.toHaveProperty("overrides");
    await api.approvePlan(id);
    // The mock adds the Target in its run.
    for (let i = 0; i < 200; i++) {
      if (
        await api.getTarget("new1").then(
          () => true,
          () => false,
        )
      )
        break;
      await new Promise((r) => setTimeout(r, 10));
    }
    const stored = (await api.getTarget("new1")).document;
    expect(stored.spec.blocks).toEqual([{ use: "motd" }]);

    // Reload: the form shows the default, no "fixed" note.
    reset();
    show(TargetForm, { api, name: "new1" });
    await until(() => document.querySelector('[id^="use-"][id$="-text"]'));
    expect(text()).not.toContain("Fixed to the current default");

    const p = await newMotdDefault(api, "Hello from the new default.");
    expect(targetsOf(p)).toContain("new1");
  });

  it("a stored value equal to the default stays until the person follows the default", async () => {
    const api = adminApi();
    // web1 fixes motd's text to the current default.
    const web1 = (await api.getTarget("web1")).document;
    const motdDefault = (
      (await api.getBlock("motd")).document.spec.params?.properties as Record<
        string,
        { default?: unknown }
      >
    )["text"]?.default;
    const pinned = structuredClone(web1);
    for (const u of pinned.spec.blocks ?? [])
      if (u.use === "motd") u.params = { text: motdDefault };
    await api.approvePlan((await api.createPlan({ reason: "Pin.", put: [pinned] })).id);

    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api, name: "web1" });
    const follow = await until(() => button("Follow the Block default"));
    expect(text()).toContain(
      "Fixed to the current default: a new default of the Block does not reach this Target.",
    );
    // A save without the action keeps the stored value.
    type("#t-reason", "Same.");
    submit();
    await planOpened();
    let doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.spec.blocks?.find((u) => u.use === "motd")?.params).toEqual({ text: motdDefault });

    // A new Block default does not reach web1.
    const p = await newMotdDefault(api, "Hello from the new default.");
    expect(targetsOf(p)).not.toContain("web1");

    // "Follow the Block default" removes the value by the normal proposal.
    reset();
    createPlan.mockClear();
    show(TargetForm, { api, name: "web1" });
    (await until(() => button("Follow the Block default")))?.click();
    flushSync();
    expect(button("Follow the Block default")).toBeUndefined();
    type("#t-reason", "Follow the default.");
    submit();
    await planOpened();
    doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.spec.blocks?.find((u) => u.use === "motd")).not.toHaveProperty("params.text");
    expect(follow).toBeDefined();
  });
});

describe("the Image form follows the same rule (T-176, D-376)", () => {
  it.each([
    ["ubuntu-web", [{ use: "nginx" }]],
    ["debian-ct-web", [{ use: "nginx", params: {} }]],
  ])("an unchanged save of %s writes no default", async (name, blocks) => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(ImageForm, { api, name });
    await until(() => document.querySelector("#i-url"));
    await until(() => document.querySelector('[id^="iuse-"]'));
    type("#i-reason", "Same.");
    submit();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as { spec: { build?: { blocks: unknown } } };
    expect(doc.spec.build?.blocks).toEqual(blocks);
  });
});

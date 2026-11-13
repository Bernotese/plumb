// @vitest-environment happy-dom
// T-118: the Block pickers offer only Blocks that fit the guest type (D-260).
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import ImageForm from "./ImageForm.svelte";
import TargetForm from "./TargetForm.svelte";
import { el, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const choices = (): string[] =>
  [...el<HTMLSelectElement>("#add-choice").options].map((o) => o.value);

describe("Target form Block picker", () => {
  it("hides a VM-only Block for a container and offers it again for a VM", async () => {
    show(TargetForm, { api: adminApi() });
    await until(() => document.querySelector("#t-image"));
    expect(choices()).toContain("net-tuning");
    expect(document.querySelector("#blocks-hidden")).toBeNull();

    type("#t-image", "debian-ct");
    flushSync();
    expect(choices()).not.toContain("net-tuning");
    expect(choices()).toContain("deploy-user");
    expect(text(el("#blocks-hidden"))).toContain("Not offered for a container: net-tuning.");

    type("#t-image", "debian-13");
    flushSync();
    expect(choices()).toContain("net-tuning");
    expect(document.querySelector("#blocks-hidden")).toBeNull();
  });

  it("moves the choice off a Block that the new type hides", async () => {
    show(TargetForm, { api: adminApi() });
    await until(() => document.querySelector("#t-image"));
    type("#add-choice", "net-tuning");
    type("#t-image", "debian-ct");
    flushSync();
    expect(el<HTMLSelectElement>("#add-choice").value).not.toBe("net-tuning");
    expect(choices()).toContain(el<HTMLSelectElement>("#add-choice").value);
  });

  it("refuses to save a container Target that uses a VM-only Block", async () => {
    const api = adminApi();
    const stored = await api.getTarget("ct-web1");
    vi.spyOn(api, "getTarget").mockResolvedValue({
      ...stored,
      document: {
        ...stored.document,
        spec: { ...stored.document.spec, blocks: [{ use: "net-tuning" }] },
      },
    });
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api, name: "ct-web1" });
    await until(() => document.querySelector(".use-unfit"));
    expect(text(el(".use-unfit"))).toContain(
      'This Block does not fit a container. It uses the resource type "sysctl", which a container does not support.',
    );
    type("#t-reason", "x");
    el<HTMLFormElement>("form").requestSubmit();
    flushSync();
    await new Promise((r) => setTimeout(r, 0));
    expect(createPlan).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(el(".use-unfit"));
  });
});

describe("Image form Block picker", () => {
  it("hides a VM-only Block for a container build", async () => {
    show(ImageForm, { api: adminApi() });
    await until(() => document.querySelector("#i-build"));
    el<HTMLInputElement>("#i-build").click();
    flushSync();
    expect(choices()).toContain("net-tuning");
    el<HTMLInputElement>('input[name="i-type"][value="lxc"]').click();
    flushSync();
    expect(choices()).not.toContain("net-tuning");
    expect(text(el("#i-not-for-kind"))).toContain("Not offered for a container: net-tuning.");
  });
});

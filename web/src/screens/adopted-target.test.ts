// @vitest-environment happy-dom
// T-194 (review F2 of #303; D-386, D-388): the Target form of an adopted
// Target. Delete lets the guest go; deleting the guest is a separate,
// destructive choice (delete_guest) that the person makes by its own plan.
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { TargetDoc, TargetView } from "../lib/types";
import TargetForm from "./TargetForm.svelte";
import { button, el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(reset);

/** The mock with web1 turned into an adopted Target (no Image). */
async function adoptedApi(deleteGuest: boolean) {
  const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
  const web1 = await api.getTarget("web1");
  const spec = structuredClone(web1.document.spec) as TargetDoc["spec"];
  delete (spec as { image?: string }).image;
  spec.adopted = { node: "pve", vmid: 9123, delete_guest: deleteGuest };
  const view: TargetView = { ...web1, document: { ...web1.document, spec } };
  api.getTarget = async () => structuredClone(view);
  return api;
}

describe("delete texts (review F2 of #303)", () => {
  it("a Target that Plumb made: the guest and its disks go", async () => {
    show(TargetForm, { api: createMockApi({ signedIn: true, stepDelayMs: 0 }), name: "web1" });
    await until(() => document.querySelector("#delete-made"));
    expect(text(el("#delete-made"))).toContain("and its disks in Proxmox");
    expect(text()).not.toContain("Plumb only lets it go");
    expect(document.querySelector("#allow-guest-delete")).toBeNull();
  });

  it("adopted, delete_guest false: Plumb lets the guest go; it stays in Proxmox", async () => {
    show(TargetForm, { api: await adoptedApi(false), name: "web1" });
    await until(() => document.querySelector("#delete-adopted-keep"));
    expect(text(el("#delete-adopted-keep"))).toContain("The plan lets the guest 9123 go");
    expect(text(el("#delete-adopted-keep"))).toContain("stay in Proxmox");
    expect(document.querySelector("#delete-made, #delete-adopted-guest")).toBeNull();
    // The separate, destructive choice is its own section.
    expect(text(el("#allow-guest-delete"))).toContain(
      "deletes the Proxmox guest 9123 and its disks",
    );
  });

  it("adopted, delete_guest true: the plan deletes the Proxmox guest", async () => {
    show(TargetForm, { api: await adoptedApi(true), name: "web1" });
    await until(() => document.querySelector("#delete-adopted-guest"));
    expect(text(el("#delete-adopted-guest"))).toContain(
      "The plan deletes the Proxmox guest 9123 and its disks. You allowed this with delete_guest. This cannot be undone.",
    );
    expect(document.querySelector("#delete-adopted-keep, #allow-guest-delete")).toBeNull();
  });

  it("'Allow Plumb to delete the guest' is a confirmed plan that sets delete_guest", async () => {
    const api = await adoptedApi(false);
    const createPlan = vi.spyOn(api, "createPlan").mockResolvedValue(await api.getPlan(14));
    show(TargetForm, { api, name: "web1" });
    (await until(() => button("Allow Plumb to delete the guest…")))?.click();
    flushSync();
    expect(createPlan).not.toHaveBeenCalled();
    button("Make the plan")?.click();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.spec.adopted).toEqual({ node: "pve", vmid: 9123, delete_guest: true });
    expect(doc.spec).not.toHaveProperty("image");
  });
});

describe("the form of an adopted Target", () => {
  it("shows no Image, no re-enroll, and saves without an image", async () => {
    const api = await adoptedApi(false);
    const createPlan = vi.spyOn(api, "createPlan").mockResolvedValue(await api.getPlan(14));
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#t-adopted"));
    expect(text(el("#t-adopted"))).toContain("adopted guest 9123 on pve");
    expect(document.querySelector("#t-image")).toBeNull();
    expect(document.querySelector("#reenroll-adopted")).not.toBeNull();
    type("#t-reason", "More memory.");
    el<HTMLFormElement>("form").requestSubmit();
    flushSync();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.spec).not.toHaveProperty("image");
    expect(doc.spec.adopted).toEqual({ node: "pve", vmid: 9123, delete_guest: false });
  });
});

describe("not adopted (T-204, D-395)", () => {
  it("shows the banner with both actions when the adopt run did not bind", async () => {
    const api = await adoptedApi(false);
    const get = api.getTarget;
    api.getTarget = async (n: string) => {
      const v = await get(n);
      return { ...v, status: { ...v.status, not_adopted: true } };
    };
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#not-adopted"));
    const banner = text(el("#not-adopted"));
    expect(banner).toContain("Not adopted: the guest changed");
    expect(banner).toContain("adopt the guest again");
    expect(banner).toContain("never touches the guest");
    expect(el("#not-adopted a").getAttribute("href")).toContain("adopt");
  });
  it("shows no banner for a bound adopted Target", async () => {
    show(TargetForm, { api: await adoptedApi(false), name: "web1" });
    await until(() => document.querySelector("#delete-adopted-keep"));
    expect(document.querySelector("#not-adopted")).toBeNull();
  });
});

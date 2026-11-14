// @vitest-environment happy-dom
// T-194 (D-373, D-386): adopt a guest of the pool as a new Target by a
// human-only Plan. The list says why a guest cannot be adopted; the wizard
// shows the warnings and what the Target will hold.
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/api";
import { createMockApi } from "../lib/mock/mock";
import { me } from "../lib/session.svelte";
import Adopt from "./Adopt.svelte";
import { button, el, planOpened, reset, rows, show, text, type, until } from "./testkit";

afterEach(() => {
  reset();
  me.session = null;
});
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

describe("adopt a guest of the pool", () => {
  it("lists the free guests with the reasons why one cannot be adopted", async () => {
    show(Adopt, { api: adminApi() });
    await until(() => rows().length === 2);
    expect(rows()[0]).toMatch(/wiki.*9123.*VM.*adoptable/);
    expect(text(el("#guest-9124-state"))).toContain("it is a privileged container");
    expect(text(el("#guest-9124-state"))).toContain("the disks do not fit a Target");
    expect(button("Adopt wiki (9123)")).toBeDefined();
    expect(button("Adopt old-db (9124)")).toBeUndefined();
  });

  it("the wizard shows the warnings and makes a human-only plan", async () => {
    const api = adminApi();
    const adopt = vi.spyOn(api, "adoptTarget");
    show(Adopt, { api });
    (await until(() => button("Adopt wiki (9123)")))?.click();
    await until(() => document.querySelector("#adopt-name"));
    expect(el<HTMLInputElement>("#adopt-name").value).toBe("wiki");
    expect(text(el("#adopt-warnings"))).toContain("Not sealed: its SSH host keys");
    expect(text(el("#adopt-fields"))).toMatch(/cores\s*2/);
    expect(text(el("#adopt-present"))).toContain("The Proxmox config holds description");
    type("#adopt-reason", "Bring the wiki under Plumb.");
    el<HTMLFormElement>("form").requestSubmit();
    flushSync();
    const id = await planOpened();
    expect(adopt).toHaveBeenCalledWith("wiki", {
      node: "pve",
      vmid: 9123,
      digest: "3f1c0d5e8a9b7c6d5e4f3a2b1c0d9e8f7a6b5c4d",
      reason: "Bring the wiki under Plumb.",
    });
    const p = await api.getPlan(id);
    expect([p.origin, p.risk, p.human_only]).toEqual([
      "adopt",
      "reversible",
      ["accept-guest-data"],
    ]);

    // After the approval the guest is a Target without an agent, and no longer in the list.
    await api.approvePlan(id);
    for (let i = 0; i < 200; i++) {
      if (
        await api.getTarget("wiki").then(
          () => true,
          () => false,
        )
      )
        break;
      await new Promise((r) => setTimeout(r, 10));
    }
    const t = await api.getTarget("wiki");
    expect([t.status.agent, t.status.guest_id]).toEqual(["none", "9123"]);
    expect((await api.listUnmanagedGuests()).map((g) => g.vmid)).toEqual([9124]);
  });

  it("refuses a bad name in the browser", async () => {
    const api = adminApi();
    const adopt = vi.spyOn(api, "adoptTarget");
    show(Adopt, { api });
    (await until(() => button("Adopt wiki (9123)")))?.click();
    await until(() => document.querySelector("#adopt-name"));
    type("#adopt-name", "Wiki Server");
    el<HTMLFormElement>("form").requestSubmit();
    flushSync();
    expect(adopt).not.toHaveBeenCalled();
    expect(el("#adopt-name").getAttribute("aria-invalid")).toBe("true");
  });

  it("a changed guest (409) shows the problem and reloads the list", async () => {
    const api = adminApi();
    const list = vi.spyOn(api, "listUnmanagedGuests");
    api.adoptTarget = () =>
      Promise.reject(
        new ApiError({
          type: "urn:plumb:problem:guest-changed",
          title: "Guest changed",
          status: 409,
          detail: "The config of guest 9123 changed. Reload the list.",
        }),
      );
    show(Adopt, { api });
    (await until(() => button("Adopt wiki (9123)")))?.click();
    await until(() => document.querySelector("#adopt-name"));
    el<HTMLFormElement>("form").requestSubmit();
    await until(() => text().includes("The config of guest 9123 changed."));
    await until(() => list.mock.calls.length === 2);
  });

  it("role read sees the list but no Adopt button", async () => {
    const api = createMockApi({ role: "read", stepDelayMs: 0 });
    me.session = await api.session();
    show(Adopt, { api });
    await until(() => rows().length === 2);
    expect(button("Adopt wiki (9123)")).toBeUndefined();
    expect(text(el("#adopt-role"))).toContain("Your role is read.");
  });

  it("an empty pool says how a guest gets in", async () => {
    const api = adminApi();
    api.listUnmanagedGuests = async () => [];
    show(Adopt, { api });
    await until(() => document.querySelector("#adopt-empty"));
    expect(text(el("#adopt-empty"))).toContain("Move a guest into the pool plumb in Proxmox");
  });
});

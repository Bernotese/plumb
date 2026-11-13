// @vitest-environment happy-dom
// T-118: Proxmox day 2 in the Target form: power, tags, disk grow (D-291, D-299).
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/api";
import { createMockApi } from "../lib/mock/mock";
import type { TargetDoc } from "../lib/types";
import { targetState } from "../lib/view";
import TargetForm from "./TargetForm.svelte";
import { el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const submit = () => {
  el<HTMLFormElement>("form").requestSubmit();
  flushSync();
};

describe("Target form day 2", () => {
  it("sends power: running for a new Target", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api });
    await until(() => document.querySelector("#guest-power"));
    expect(el<HTMLSelectElement>("#guest-power").value).toBe("running");
    type("#t-name", "new1");
    type("#t-reason", "New.");
    submit();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.spec.guest.power).toBe("running");
  });

  it("grows the disk and refuses a smaller one", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#guest-disk"));
    expect(el<HTMLInputElement>("#guest-disk").readOnly).toBe(false);
    type("#guest-disk", "10");
    type("#t-reason", "Shrink.");
    submit();
    expect(createPlan).not.toHaveBeenCalled();
    expect(text()).toContain("A disk only grows: at least 20 GiB");
    type("#guest-disk", "40");
    submit();
    const plan = await api.getPlan(await planOpened());
    const g = plan.targets[0]?.resources.find((r) => r.resource === "guest:config");
    expect(g?.changes).toContainEqual(
      expect.objectContaining({ attr: "disk", from: 20, to: 40, risk: "disruptive" }),
    );
  });

  it("asks for allow_restart before a stop", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#guest-power"));
    type("#guest-power", "stopped");
    type("#t-reason", "Stop.");
    submit();
    expect(createPlan).not.toHaveBeenCalled();
    expect(text()).toContain("A stop needs 'Plumb may restart this VM' below.");
    el<HTMLInputElement>("#t-allow-restart").click();
    flushSync();
    submit();
    const plan = await api.getPlan(await planOpened());
    const g = plan.targets[0]?.resources.find((r) => r.resource === "guest:config");
    expect(g?.changes).toContainEqual(
      expect.objectContaining({ attr: "power", to: "stopped", risk: "disruptive" }),
    );
  });

  it("sends an emptied tag list as [] (no tags), not as unmanaged", async () => {
    const api = adminApi();
    const stored = await api.getTarget("web1");
    vi.spyOn(api, "getTarget").mockResolvedValue({
      ...stored,
      document: {
        ...stored.document,
        spec: { ...stored.document.spec, guest: { ...stored.document.spec.guest, tags: ["web"] } },
      },
    });
    const createPlan = vi.spyOn(api, "createPlan").mockRejectedValue(new Error("stop"));
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#guest-tags-0"));
    type("#guest-tags-0", "");
    type("#t-reason", "No tags.");
    submit();
    await until(() => createPlan.mock.calls.length > 0);
    expect((createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc).spec.guest.tags).toEqual([]);
  });
});

describe("mock day 2 rules", () => {
  it("refuses a smaller disk and a stop without allow_restart", async () => {
    const api = adminApi();
    const t = await api.getTarget("web1");
    const put = (guest: Record<string, unknown>) =>
      api.createPlan({
        reason: "x",
        put: [
          {
            ...t.document,
            spec: { ...t.document.spec, guest: { ...t.document.spec.guest, ...guest } },
          },
        ],
      });
    await expect(put({ disk: 10 })).rejects.toBeInstanceOf(ApiError);
    await expect(put({ power: "stopped" })).rejects.toBeInstanceOf(ApiError);
  });

  it("shows a stopped Target as Stopped, not Offline", async () => {
    const t = await adminApi().getTarget("web1");
    const s = targetState({ ...t, status: { ...t.status, phase: "stopped", agent: "offline" } });
    expect(s.label).toBe("Stopped");
  });
});

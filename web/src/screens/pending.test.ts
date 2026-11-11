// @vitest-environment happy-dom
// Screen tests of T-056 on the mock: "restart needed" in the list and on
// the Target page, allow_restart, re-enrollment, the Plan tags, the Run
// count and the D-192 note.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { Run } from "../lib/types";
import PlanView from "./PlanView.svelte";
import RunView from "./RunView.svelte";
import TargetForm from "./TargetForm.svelte";
import Targets from "./Targets.svelte";
import { button, el, planOpened, reset, rows, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

describe("target list", () => {
  it("shows restart needed with its age, also when the phase is drifted", async () => {
    show(Targets, { api: adminApi() });
    await until(() => rows().length > 0);
    const worker = rows().find((r) => r.startsWith("worker1")) ?? "";
    const web = rows().find((r) => r.startsWith("web1")) ?? "";
    expect(worker).toMatch(/Restart needed.*waiting for /);
    expect(web).toContain("Out of Plumb");
    expect(web).toMatch(/Restart needed.*waiting for /);
    expect(rows().find((r) => r.startsWith("db1"))).not.toContain("estart needed");
  });
});

describe("target page", () => {
  it("explains the waiting values and what to do", async () => {
    show(TargetForm, { api: adminApi(), name: "worker1" });
    const s = await until(() => document.querySelector("section.waiting"));
    const t = text(s);
    expect(t).toContain("Restart needed since");
    expect(t).toContain(
      "memory: the VM uses 2048 MiB. 4096 MiB is set and becomes active at the next stop and start of the VM.",
    );
    expect(t).toContain("a reboot inside the guest is not enough");
    expect(t).toContain("switch on 'Plumb may restart this VM' and make a Plan");
  });

  it("saves allow_restart from the checkbox, and no new key by an unrelated edit", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api, name: "web1" });
    const box = await until(() => document.querySelector<HTMLInputElement>("#t-allow-restart"));
    expect(box.checked).toBe(false);
    expect(text(el("#t-allow-restart-hint"))).toBe(
      "Off: a change of cores or memory waits until you stop and start the VM. On: the Plan shows the restart, and the Run does it.",
    );
    type("#t-reason", "edit");
    button("Show the plan")?.click();
    await planOpened();
    expect(createPlan.mock.calls[0]?.[0].put?.[0]?.spec).not.toHaveProperty("allow_restart");

    reset();
    show(TargetForm, { api, name: "web1" });
    const box2 = await until(() => document.querySelector<HTMLInputElement>("#t-allow-restart"));
    box2.click();
    type("#t-reason", "allow");
    button("Show the plan")?.click();
    const id = await planOpened();
    expect(createPlan.mock.calls[1]?.[0].put?.[0]?.spec).toMatchObject({ allow_restart: true });
    const plan = await api.getPlan(id);
    const guest = plan.targets[0]?.resources.find((r) => r.resource === "guest:config");
    expect(guest?.changes).toContainEqual({
      attr: "allow_restart",
      from: false,
      to: true,
      risk: "read",
    });
  });

  it("refuses the re-enrollment of a running VM with allow_restart off, with the two ways", async () => {
    show(TargetForm, { api: adminApi(), name: "worker1" });
    await until(() => document.querySelector("#reenroll-title"));
    expect(text(el("#reenroll-refused"))).toContain("Stop the VM in Proxmox and re-enroll again");
    button("Plan the re-enrollment")?.click();
    await until(
      () =>
        /does not allow a restart by a Run/.test(text()) &&
        document.querySelectorAll("[role=alert], .problem").length,
    );
    expect(location.hash).not.toMatch(/plans/);
  });

  it("plans the re-enrollment when the setting is on, and says it stays on", async () => {
    show(TargetForm, { api: adminApi(), name: "db1" });
    await until(() => document.querySelector("#reenroll-title"));
    expect(text()).toContain(
      "'Plumb may restart this VM' stays on after it, until you switch it off.",
    );
    button("Plan the re-enrollment")?.click();
    await planOpened();
  });

  it("warns at the address field for a static address on Ubuntu 26.04 (D-192)", async () => {
    show(TargetForm, { api: adminApi() });
    await until(() => document.querySelector("#guest-ip"));
    type("#t-image", "ubuntu-26.04");
    expect(document.querySelector("#guest-ip-note")).toBeNull();
    type("#guest-ip", "192.168.1.50/24");
    expect(text(el("#guest-ip-note"))).toBe(
      "Ubuntu 26.04 takes a static address only from its second boot (D-192). Use DHCP.",
    );
    expect(el("#guest-ip").getAttribute("aria-describedby")).toContain("guest-ip-note");
    type("#t-image", "debian-13");
    expect(document.querySelector("#guest-ip-note")).toBeNull();
  });
});

describe("plan and run", () => {
  it("tags a memory change that waits", async () => {
    const api = adminApi();
    const t = await api.getTarget("web1");
    const spec = { ...t.document.spec, guest: { ...t.document.spec.guest, memory: 4096 } };
    const p = await api.createPlan({
      reason: "more",
      put: [{ kind: "Target", name: "web1", spec }],
    });
    show(PlanView, { api, id: p.id });
    await until(() => /after restart/.test(text()));
    expect(text()).toContain(
      "Plumb writes 4096 to Proxmox. The VM keeps 2048 until its next stop and start.",
    );
  });

  it("shows the restart of the VM when allow_restart is on", async () => {
    const api = adminApi();
    const t = await api.getTarget("db1");
    const spec = { ...t.document.spec, guest: { ...t.document.spec.guest, cores: 8 } };
    const p = await api.createPlan({
      reason: "cores",
      put: [{ kind: "Target", name: "db1", spec }],
    });
    show(PlanView, { api, id: p.id });
    await until(() => /hard power-off after 2 minutes/.test(text()));
    expect(text()).not.toContain("after restart");
  });

  it("counts the changes that wait in the Run", async () => {
    const api = adminApi();
    const run = await api.getRun(3);
    vi.spyOn(api, "getRun").mockResolvedValue({ ...run, waits_for_restart: 2 } as Run);
    show(RunView, { api, id: 3 });
    const p = await until(() => document.querySelector("#run-waits"));
    expect(text(p)).toContain("2 changes wait for a restart of the VM");
  });
});

// @vitest-environment happy-dom
// T-239 (ARCHITECTURE §21, D-423, D-436): the data disk list of the Target
// form on the mock. The slot is the disk's identity; a missing `disks` is
// not managed; a remove is destructive and has a second (free) step.
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { Plan, TargetDoc, TargetView } from "../lib/types";
import TargetForm from "./TargetForm.svelte";
import { button, el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const submit = () => {
  el<HTMLFormElement>("form").requestSubmit();
  flushSync();
};
async function edit(name = "db1", api = adminApi()) {
  const createPlan = vi.spyOn(api, "createPlan");
  show(TargetForm, { api, name });
  await until(() => document.querySelector("#disks"));
  await new Promise((r) => setTimeout(r, 10));
  flushSync();
  return {
    api,
    createPlan,
    doc: () => createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc,
    plan: async (): Promise<Plan> => api.getPlan(await planOpened()),
  };
}
const diskLines = (p: Plan) =>
  p.targets[0]?.resources
    .find((r) => r.resource === "guest:config")
    ?.changes.filter((c) => /^disk[1-3]$/.test(c.attr)) ?? [];

describe("data disk list (VM)", () => {
  it("shows the stored disk and keeps it on an unrelated save", async () => {
    const { doc, plan } = await edit();
    expect(el<HTMLInputElement>("#disks-managed").checked).toBe(true);
    expect(el("#disk-1-slot").textContent).toBe("1 (scsi1)"); // fixed: not a select
    expect(el<HTMLInputElement>("#disk-1-size").value).toBe("50");
    expect(document.querySelector("#disk-1-path")).toBeNull(); // a VM disk has no path
    expect(text(el("#disks"))).toContain("lsblk -o NAME,SERIAL");
    type("#t-reason", "No disk change.");
    submit();
    const p = await plan();
    expect(doc().spec.guest.disks).toEqual([{ slot: 1, size_gb: 50 }]);
    expect(diskLines(p)).toEqual([]);
  });

  it("adds a disk in a free slot: reversible", async () => {
    const { doc, plan } = await edit();
    el<HTMLButtonElement>("#add-disk").click();
    await until(() => document.activeElement?.id === "disk-2-size");
    expect([...el<HTMLSelectElement>("#disk-2-slot").options].map((o) => o.value)).toEqual([
      "2",
      "3",
    ]);
    type("#disk-2-slot", "3");
    type("#disk-2-size", "20");
    type("#t-reason", "A data disk for the WAL.");
    submit();
    const p = await plan();
    expect(doc().spec.guest.disks).toEqual([
      { slot: 1, size_gb: 50 },
      { slot: 3, size_gb: 20 },
    ]);
    expect(diskLines(p)).toEqual([{ attr: "disk3", to: "20 GiB", risk: "reversible" }]);
  });

  it("grows a disk (disruptive) and refuses a smaller size at the field", async () => {
    const { createPlan, plan } = await edit();
    type("#disk-1-size", "40");
    type("#t-reason", "Smaller.");
    submit();
    expect(el("#disk-1-size-error").textContent).toContain("only grows: at least 50 GiB");
    expect(el("#disk-1-size").getAttribute("aria-invalid")).toBe("true");
    expect(createPlan).not.toHaveBeenCalled();
    type("#disk-1-size", "80");
    submit();
    expect(diskLines(await plan())).toEqual([
      { attr: "disk1", from: "50 GiB", to: "80 GiB", risk: "disruptive" },
    ]);
  });

  it("removes a disk: destructive, the data is lost, a second step frees it", async () => {
    const { doc, plan } = await edit();
    button("Remove the disk in slot 1")?.click();
    await until(() => document.activeElement?.id === "add-disk");
    const banner = text(el("#disks-removed"));
    expect(banner).toContain("Slot 1: 50 GiB");
    expect(banner).toContain("the data is lost");
    expect(banner).toContain("second step of the same Run");
    type("#t-reason", "The disk is not needed.");
    submit();
    const p = await plan();
    // [] keeps the field: Plumb manages the slots and removes the disk.
    expect(doc().spec.guest.disks).toEqual([]);
    expect(diskLines(p)).toEqual([{ attr: "disk1", from: "50 GiB", risk: "destructive" }]);
    expect(p.warnings.join("\n")).toContain(
      "the data on disk 1 (local-lvm:vm-9002-disk-1, 50 GiB) is lost. Plumb detaches the disk, then frees its volume in a second step of the same Run.",
    );
  });

  it("a remove on a running guest needs allow_restart", async () => {
    const api = adminApi();
    const db1 = await api.getTarget("db1");
    const view: TargetView = structuredClone(db1);
    view.document.spec.allow_restart = false;
    api.getTarget = async () => structuredClone(view);
    const { createPlan } = await edit("db1", api);
    button("Remove the disk in slot 1")?.click();
    type("#t-reason", "Remove.");
    submit();
    expect(el("#disks-remove-error").textContent).toContain("needs");
    expect(createPlan).not.toHaveBeenCalled();
  });

  it("switching management off deletes nothing: the field goes, no disk line", async () => {
    const { doc, plan } = await edit();
    el<HTMLInputElement>("#disks-managed").click();
    flushSync();
    expect(document.querySelector("#disk-1-size")).toBeNull();
    expect(el("#disks-managed-hint").textContent).toContain("Switching it off deletes nothing");
    type("#t-reason", "Manage the disks by hand.");
    submit();
    const p = await plan();
    expect(doc().spec.guest).not.toHaveProperty("disks");
    expect(diskLines(p)).toEqual([]);
  });

  it("a Target without disks has no `disks` until a disk is added", async () => {
    const { doc } = await edit("web1");
    expect(el<HTMLInputElement>("#disks-managed").checked).toBe(false);
    type("#t-reason", "Unrelated.");
    submit();
    await planOpened();
    expect(doc().spec.guest).not.toHaveProperty("disks");
  });

  it("at most 3 data disks", async () => {
    await edit();
    el<HTMLButtonElement>("#add-disk").click();
    flushSync();
    el<HTMLButtonElement>("#add-disk").click();
    flushSync();
    expect(document.querySelector("#add-disk")).toBeNull();
    expect(document.querySelectorAll("fieldset.disk")).toHaveLength(3);
  });

  it("an adopted Target has no disk list", async () => {
    const api = adminApi();
    const web1 = await api.getTarget("web1");
    const view: TargetView = structuredClone(web1);
    delete (view.document.spec as { image?: string }).image;
    view.document.spec.adopted = { node: "pve", vmid: 9123, delete_guest: false };
    api.getTarget = async () => structuredClone(view);
    await edit("web1", api);
    expect(el("#disks-adopted").textContent).toContain("Plumb adds none");
    expect(document.querySelector("#add-disk, #disks-managed")).toBeNull();
  });
});

describe("data disk list (container)", () => {
  it("shows the stored path as create-only and checks a new path", async () => {
    const { doc, plan } = await edit("ct1");
    expect(el("#disk-1-path").tagName).toBe("SPAN");
    expect(el("#disk-1-path").textContent).toBe("/srv/data");
    expect(el("#disk-1-slot").textContent).toBe("1 (mp1)");
    el<HTMLButtonElement>("#add-disk").click();
    await until(() => document.querySelector("#disk-2-path"));
    type("#t-reason", "A second disk.");
    const tries: [string, string][] = [
      ["", "needs a path"],
      ["srv/x", "Give an absolute path"],
      ["/srv/../etc", "no . or .. part"],
      ["/etc/app", "/etc is a system tree"],
      ["/lib64/x", "/lib64 is a system tree"],
      ["/srv/data/sub", "inside the path /srv/data of disk 1"],
      ["/srv", "inside the path /srv/data of disk 1"],
    ];
    for (const [path, msg] of tries) {
      type("#disk-2-path", path);
      submit();
      expect(el("#disk-2-path-error").textContent, path).toContain(msg);
    }
    type("#disk-2-path", "/opt/cache");
    submit();
    const p = await plan();
    expect(doc().spec.guest.disks).toEqual([
      { slot: 1, size_gb: 10, path: "/srv/data" },
      { slot: 2, size_gb: 10, path: "/opt/cache" },
    ]);
    expect(diskLines(p)).toEqual([
      { attr: "disk2", to: "10 GiB at /opt/cache", risk: "reversible" },
    ]);
    expect(text(el("#disks-restore"))).toContain("a container loses the data on that disk");
  });
});

describe("restore of a backup older than a disk (mock)", () => {
  it("a VM keeps the disk as an unused volume; a newer backup names none", async () => {
    const api = adminApi();
    const old = await api.restoreGuest("db1", { backup: "1a2b3c4d5e6f7a8b", reason: "Old." });
    const lines = old.targets[0]?.resources[0]?.changes.filter((c) => c.attr === "disk1");
    expect(lines).toEqual([
      { attr: "disk1", from: "50 GiB", to: "kept as an unused volume", risk: "destructive" },
    ]);
    expect(old.warnings.join("\n")).toContain(
      "disk 1 (local-lvm:vm-9002-disk-1) is detached and kept as an unused volume",
    );
    const fresh = await api.restoreGuest("db1", { backup: "3f2a9c0d1e4b5a67", reason: "New." });
    expect(fresh.warnings.join("\n")).not.toContain("disk 1");
    expect(fresh.targets[0]?.resources[0]?.changes.some((c) => c.attr === "disk1")).toBe(false);
  });
});

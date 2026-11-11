// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { TargetView } from "../lib/types";
import ChangeTable from "../ui/ChangeTable.svelte";
import TargetForm from "./TargetForm.svelte";
import Targets from "./Targets.svelte";
import { button, el, planOpened, reset, rows, show, text, until } from "./testkit";

afterEach(reset);
const phases = [
  "pending",
  "provisioning",
  "enrolling",
  "ready",
  "applying",
  "drifted",
  "restart_needed",
  "error",
] satisfies TargetView["status"]["phase"][];

it.each(phases)("keeps the waiting badge, age and section in phase %s", async (phase) => {
  const api = createMockApi({ signedIn: true });
  const target = await api.getTarget("worker1");
  target.status.phase = phase;
  target.status.waiting = [{ attr: "memory", from: 2048, to: 4096, since: "2026-11-10T08:00:00Z" }];
  vi.spyOn(api, "listTargets").mockResolvedValue([target]);
  vi.spyOn(api, "getTarget").mockResolvedValue(target);
  show(Targets, { api });
  await until(() => rows().length > 0);
  expect(rows()[0]).toMatch(/Restart needed.*waiting for /);
  expect(el("tbody .ignored[title]").getAttribute("title")).toContain("2026");
  reset();
  show(TargetForm, { api, name: "worker1" });
  const section = await until(() => document.querySelector("section.waiting"));
  expect(text(section)).toContain("Restart needed since");
  expect(text(section)).toContain("4096 MiB is set");
});

it("defaults a new Target to no restart", async () => {
  show(TargetForm, { api: createMockApi({ signedIn: true }) });
  const box = await until(() => document.querySelector<HTMLInputElement>("#t-allow-restart"));
  expect(box.checked).toBe(false);
  expect(text(box.closest("label") ?? document.body).trim()).toBe("Plumb may restart this VM");
});

it("plans re-enrollment while stopped with restarts disabled", async () => {
  const api = createMockApi({ signedIn: true });
  const target = await api.getTarget("worker1");
  target.status.power = "stopped";
  target.document.spec.allow_restart = false;
  vi.spyOn(api, "getTarget").mockResolvedValue(target);
  const reenroll = vi
    .spyOn(api, "reenrollTarget")
    .mockResolvedValue({ ...(await api.reenrollTarget("db1", "fixture")), id: 77 });
  show(TargetForm, { api, name: "worker1" });
  await until(() => document.querySelector("#reenroll-title"));
  expect(document.querySelector("#reenroll-refused")).toBeNull();
  expect(text()).toContain("The VM is stopped: the Run starts it with the new token.");
  button("Plan the re-enrollment")?.click();
  expect(await planOpened()).toBe(77);
  expect(reenroll).toHaveBeenCalledWith("worker1", "");
});

it("renders pending, also-pending and create-only guidance in the change table", async () => {
  show(ChangeTable, {
    caption: "Pending drift",
    resource: "guest:config",
    changes: [
      { attr: "memory", from: 256, to: 1024, risk: "reversible", pending: true },
      { attr: "cores", from: 8, to: 2, risk: "disruptive", also_pending: 4 },
      { attr: "ip", from: "10.0.0.9/24", to: "dhcp", risk: "destructive", pending: true },
      { attr: "allow_restart", from: false, to: true, risk: "read" },
    ],
  });
  await until(() => document.querySelector(".tag"));
  expect([...document.querySelectorAll(".tag")].map(text)).toEqual(["pending", "pending"]);
  expect(text()).toContain("The VM uses 1024. 256 becomes active");
  expect(text()).toContain("Also pending: 4.");
  expect(text()).toContain("Plumb does not revert this value. Remove it in Proxmox");
  expect(text()).toContain("qm set <vmid> --revert <key>");
  expect(rows()).toContainEqual(expect.stringMatching(/Plumb may restart this VM.*no.*yes/));
});

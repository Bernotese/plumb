// @vitest-environment happy-dom
// T-223 (ARCHITECTURE §20, D-399 … D-404, D-408, D-417): the Target form's
// NIC list, its backup schedule, and the note of #283 n1.
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { BlockView, TargetDoc } from "../lib/types";
import TargetForm from "./TargetForm.svelte";
import { button, el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const submit = () => {
  el<HTMLFormElement>("form").requestSubmit();
  flushSync();
};
async function edit(api = adminApi(), name = "db1") {
  const createPlan = vi.spyOn(api, "createPlan");
  show(TargetForm, { api, name });
  await until(() => document.querySelector("#add-nic, #nics-adopted"));
  // The allowed networks come from the settings.
  await new Promise((r) => setTimeout(r, 10));
  flushSync();
  return { api, createPlan, doc: () => createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc };
}

describe("NIC list", () => {
  it("shows the stored card and keeps it on an unrelated save", async () => {
    const { doc } = await edit();
    expect(el<HTMLSelectElement>("#nic-1-bridge").value).toBe("vmbr2");
    expect(el<HTMLSelectElement>("#nic-1-tag").value).toBe("20");
    expect(el<HTMLInputElement>("#nic-1-ip").value).toBe("10.20.0.20/24");
    type("#t-reason", "No change of the NICs.");
    submit();
    await planOpened();
    expect(doc().spec.guest.nics).toEqual([{ bridge: "vmbr2", tag: 20, ip: "10.20.0.20/24" }]);
  });

  it("adds a card on a named bridge; tags follow the bridge", async () => {
    const { doc } = await edit();
    el<HTMLButtonElement>("#add-nic").click();
    await until(() => document.activeElement?.id === "nic-2-bridge");
    expect([...el<HTMLSelectElement>("#nic-2-bridge").options].map((o) => o.value)).toEqual([
      "vmbr1",
      "vmbr2",
    ]);
    expect([...el<HTMLSelectElement>("#nic-2-tag").options].map((o) => o.textContent)).toEqual([
      "(untagged)",
    ]);
    type("#nic-2-bridge", "vmbr2");
    expect([...el<HTMLSelectElement>("#nic-2-tag").options].map((o) => o.value)).toEqual([
      "20",
      "30",
    ]);
    type("#nic-2-tag", "30");
    type("#nic-2-addr", "dhcp");
    type("#t-reason", "A second VLAN.");
    submit();
    await planOpened();
    expect(doc().spec.guest.nics).toEqual([
      { bridge: "vmbr2", tag: 20, ip: "10.20.0.20/24" },
      { bridge: "vmbr2", tag: 30, ip: "dhcp" },
    ]);
  });

  it("removing every card removes the key", async () => {
    const { doc } = await edit();
    button("Remove network card 2")?.click();
    await until(() => document.activeElement?.id === "add-nic");
    type("#t-reason", "No second NIC.");
    submit();
    await planOpened();
    expect(doc().spec.guest).not.toHaveProperty("nics");
  });

  it("at most 3 more cards", async () => {
    await edit();
    el<HTMLButtonElement>("#add-nic").click();
    flushSync();
    el<HTMLButtonElement>("#add-nic").click();
    flushSync();
    expect(document.querySelector("#nic-3-bridge")).not.toBeNull();
    expect(document.querySelector("#add-nic")).toBeNull();
  });

  it("refuses a second gateway and a bad address", async () => {
    const { createPlan } = await edit();
    type("#nic-1-gateway", "10.20.0.1");
    el<HTMLButtonElement>("#add-nic").click();
    flushSync();
    type("#nic-2-addr", "static");
    type("#nic-2-ip", "10.0.0.5");
    type("#nic-2-gateway", "10.0.0.1");
    type("#t-reason", "Two gateways.");
    submit();
    expect(createPlan).not.toHaveBeenCalled();
    expect(text(el("#nic-2-ip-error"))).toContain("CIDR");
    expect(text(el("#nic-2-gateway-error"))).toContain("Only one network card may have a gateway.");
    expect(el("#nic-2-gateway").getAttribute("aria-describedby")).toContain("nic-2-gateway-error");
  });

  it("a stored bridge the install did not name is shown as text and refused", async () => {
    const api = adminApi();
    const t = await api.getTarget("db1");
    t.document.spec.guest.nics = [{ bridge: "vmbr7" }];
    vi.spyOn(api, "getTarget").mockResolvedValue(t);
    const { createPlan } = await edit(api);
    expect(el<HTMLInputElement>("#nic-1-bridge").tagName).toBe("INPUT");
    type("#t-reason", "Keep it.");
    submit();
    expect(createPlan).not.toHaveBeenCalled();
    expect(text(el("#nic-1-bridge-error"))).toContain("did not name the bridge vmbr7");
  });

  it("without the settings, bridge and tag are free text; the server checks", async () => {
    const api = adminApi();
    vi.spyOn(api, "getSettings").mockRejectedValue(new Error("no"));
    const { doc } = await edit(api);
    el<HTMLButtonElement>("#add-nic").click();
    flushSync();
    type("#nic-2-bridge", "vmbr5");
    type("#nic-2-tag", "40");
    type("#t-reason", "Free text.");
    submit();
    await planOpened();
    expect(doc().spec.guest.nics?.[1]).toEqual({ bridge: "vmbr5", tag: 40 });
  });

  it("warns of network rights beyond the install, not of other extras (M2 of #330)", async () => {
    const api = adminApi();
    const get = api.getSettings.bind(api);
    vi.spyOn(api, "getSettings").mockImplementation(async () => {
      const s = await get();
      if (s.proxmox)
        s.proxmox.acl_extra = [
          { path: "/sdn/zones/localnetwork/vmbr9", role: "PlumbBridge" },
          { path: "/storage/old-backup", role: "PlumbGuestBackup" },
        ];
      return s;
    });
    await edit(api);
    await until(() => document.querySelector("#nics-acl-extra"));
    expect(text(el("#nics-acl-extra"))).toContain("/sdn/zones/localnetwork/vmbr9 (PlumbBridge)");
    expect(text(el("#nics-acl-extra"))).not.toContain("old-backup");
  });

  it("an adopted Target offers no card", async () => {
    const api = adminApi();
    const t = await api.getTarget("web1");
    t.document.spec.adopted = { vmid: 9001 } as never;
    vi.spyOn(api, "getTarget").mockResolvedValue(t);
    await edit(api, "web1");
    expect(document.querySelector("#add-nic")).toBeNull();
    expect(text(el("#nics-adopted"))).toContain("Plumb adds none");
  });
});

describe("backup schedule in the form", () => {
  it("lowering keep says the plan is destructive; unticking removes the field", async () => {
    const { doc } = await edit();
    expect(el<HTMLInputElement>("#backup-on").checked).toBe(true);
    type("#backup-keep", "3");
    expect(text(el("#backup-keep-lower"))).toContain(
      "when the next backup job would delete backups with this value, the plan is destructive",
    );
    el<HTMLInputElement>("#backup-on").click();
    flushSync();
    expect(text(el("#backup-off-note"))).toContain("It deletes none");
    type("#t-reason", "No more backups.");
    submit();
    await planOpened();
    expect(doc().spec).not.toHaveProperty("backup");
  });

  it("says when the install named no backup storage (serve --dev)", async () => {
    const api = adminApi();
    const get = api.getSettings.bind(api);
    vi.spyOn(api, "getSettings").mockImplementation(async () => {
      const s = await get();
      if (s.proxmox) s.proxmox.guest_backup = null;
      return s;
    });
    await edit(api, "web1");
    await until(() => document.querySelector("#backup-no-storage"));
    expect(text(el("#backup-no-storage"))).toContain("a schedule makes no backup until it does");
  });

  it("no such note with a storage", async () => {
    await edit(adminApi(), "web1");
    expect(document.querySelector("#backup-no-storage")).toBeNull();
  });

  it("writes a weekly schedule and checks time and keep", async () => {
    const { doc, createPlan } = await edit(adminApi(), "web1");
    el<HTMLInputElement>("#backup-on").click();
    flushSync();
    type("#backup-schedule", "weekly");
    type("#backup-weekday", "sat");
    type("#backup-time", "25:00");
    type("#backup-keep", "31");
    type("#t-reason", "Weekly backups.");
    submit();
    expect(createPlan).not.toHaveBeenCalled();
    expect(document.querySelector("#backup-time-error")).not.toBeNull();
    expect(document.querySelector("#backup-keep-error")).not.toBeNull();
    type("#backup-time", "02:30");
    type("#backup-keep", "4");
    submit();
    await planOpened();
    expect(doc().spec.backup).toEqual({
      schedule: "weekly",
      weekday: "sat",
      time: "02:30",
      keep: 4,
    });
  });
});

describe("#283 n1: an object parameter whose parts have their own defaults", () => {
  async function withBlock(def: Record<string, unknown>) {
    const api = adminApi();
    const blocks = await api.listBlocks();
    const motd = structuredClone(blocks.find((b) => b.document.name === "motd")) as BlockView;
    motd.document.name = "limits";
    motd.document.spec.params = {
      type: "object",
      properties: {
        limits: {
          type: "object",
          default: def,
          properties: {
            soft: { type: "integer", default: 1 },
            hard: { type: "integer", default: 2 },
          },
        },
      },
    } as never;
    vi.spyOn(api, "listBlocks").mockResolvedValue([...blocks, motd]);
    show(TargetForm, { api });
    await until(() => document.querySelector("#add-choice"));
    type("#add-choice", "limits");
    button("Add Block")?.click();
    await until(() => document.querySelector('[id^="use-"][id$="-limits-soft"]'));
  }

  it("says it is saved with all its parts when the default leaves a part out", async () => {
    await withBlock({ soft: 1 });
    const note = el('[id^="use-"][id$="-limits-note"]');
    expect(text(note)).toContain("Saved with all its parts");
  });

  it("no note when the default names every part", async () => {
    await withBlock({ soft: 1, hard: 2 });
    expect(document.querySelector('[id^="use-"][id$="-limits-note"]')).toBeNull();
  });
});

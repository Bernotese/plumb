// @vitest-environment happy-dom
// T-176 (D-361): Settings → General: the backup schedule with its sources,
// editable by a human admin, and the read-only Proxmox view.
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import General from "./General.svelte";
import { button, el, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const save = () => {
  el<HTMLFormElement>("#general-backup form").requestSubmit();
  flushSync();
};

describe("Settings → General", () => {
  it("says that a time moved earlier can skip one day (#282 n1)", async () => {
    show(General, { api: adminApi() });
    await until(() => document.querySelector("#g-time-hint"));
    expect(text(el("#g-time-hint"))).toContain("can skip one backup day");
  });

  it("shows the schedule with its source and the Proxmox view, no secret", async () => {
    const api = adminApi();
    const s = await api.getSettings();
    show(General, { api });
    await until(() => document.querySelector("#g-time"));
    expect(el<HTMLInputElement>("#g-time").value).toBe(s.backup.time);
    expect(el<HTMLInputElement>("#g-keep").value).toBe(String(s.backup.keep));
    expect(el("#g-time-hint").textContent).toContain("from the install (flag or environment)");
    expect(document.querySelector("#g-time-reset")).toBeNull();
    const px = el("#general-proxmox");
    expect(text(px)).toContain(String(s.proxmox?.node));
    expect(text(px)).toContain(String(s.proxmox?.token_user));
    expect(text(px)).toContain("Plumb never shows the token name or its secret.");
    expect(px.querySelectorAll("tbody tr").length).toBe(s.proxmox?.acl_lines.length);
  });

  it("saves a new time, then goes back to the install value", async () => {
    const api = adminApi();
    const change = vi.spyOn(api, "changeSettings");
    const install = (await api.getSettings()).backup.time;
    show(General, { api });
    await until(() => document.querySelector("#g-time"));
    type("#g-time", "02:30");
    save();
    await until(() => el("#g-saved").textContent?.includes("Saved."));
    expect(change).toHaveBeenLastCalledWith({ backup: { time: "02:30" } });
    expect(el("#g-saved").textContent).toContain("The next backup runs at 02:30 UTC");
    expect(el("#g-time-hint").textContent).toContain("set here");
    expect((await api.listBackups()).schedule.time).toBe("02:30");

    button("Use the install value again")?.click();
    await until(() => el<HTMLInputElement>("#g-time").value === install);
    expect(change).toHaveBeenLastCalledWith({ backup: { time: null } });
    expect(el("#g-time-hint").textContent).toContain("from the install");
  });

  it("refuses a wrong time and keep at the field, without a call", async () => {
    const api = adminApi();
    const change = vi.spyOn(api, "changeSettings");
    show(General, { api });
    await until(() => document.querySelector("#g-time"));
    type("#g-time", "25:00");
    type("#g-keep", "0");
    save();
    expect(change).not.toHaveBeenCalled();
    expect(el("#g-time").getAttribute("aria-invalid")).toBe("true");
    expect(el("#g-time-error").textContent).toContain("HH:MM");
    expect(el("#g-keep-error").textContent).toBe("Keep 1 to 100 backups.");
    expect(el("#g-time").getAttribute("aria-describedby")).toBe("g-time-error g-time-hint");
  });

  it("an unchanged form sends nothing", async () => {
    const api = adminApi();
    const change = vi.spyOn(api, "changeSettings");
    show(General, { api });
    await until(() => document.querySelector("#g-time"));
    save();
    expect(change).not.toHaveBeenCalled();
    expect(el("#g-saved").textContent).toBe("Nothing to save: the values are the same.");
  });

  it("without the Proxmox view (a token) says why", async () => {
    const api = adminApi();
    const s = await api.getSettings();
    api.getSettings = async () => ({ ...s, proxmox: null });
    show(General, { api });
    await until(() => document.querySelector("#general-proxmox"));
    expect(text(el("#general-proxmox"))).toContain(
      "Only a person who logged in sees the Proxmox connection.",
    );
  });

  it("shows a missing right and the repair hint", async () => {
    const api = adminApi();
    const s = await api.getSettings();
    const px = s.proxmox;
    if (!px) throw new Error("no proxmox view");
    api.getSettings = async () => ({
      ...s,
      proxmox: {
        ...px,
        acl: "missing",
        acl_lines: [
          { path: "/pool/plumb", role: "PlumbGuests", ok: true },
          { path: "/", role: "PlumbAudit", ok: false },
        ],
      },
    });
    show(General, { api });
    await until(() => document.querySelector("#px-acl-help"));
    expect(text(el("#px-acl"))).toContain("rights missing");
    expect(text(el("#general-proxmox tbody"))).toMatch(/\/\s*PlumbAudit\s*missing/);
  });
});

describe("Settings → General: repair key (T-194, D-382)", () => {
  it("lists the Targets on the previous key, revokes it after a confirm", async () => {
    const api = adminApi();
    const rotate = vi.spyOn(api, "rotateRepairKey");
    show(General, { api });
    await until(() => document.querySelector("#general-key"));
    expect(el("#key-current").textContent).toMatch(/^SHA256:/);
    expect(text(el("#key-old-targets"))).toContain("Still on the previous key (1): worker1.");
    button("Revoke the previous key…")?.click();
    flushSync();
    expect(text(el("#revoke-q"))).toContain("worker1 then has no repair over SSH");
    button("Revoke")?.click();
    await until(() => document.querySelector("#key-rotate"));
    expect(rotate).toHaveBeenLastCalledWith(true);
    expect(document.querySelector("#key-previous")).toBeNull();

    // A new key: the old one stays until a person revokes it.
    button("Make a new repair key")?.click();
    await until(() => document.querySelector("#key-previous"));
    expect(rotate).toHaveBeenLastCalledWith(false);
    expect(text(el("#general-key"))).not.toContain("BEGIN");
  });

  it("shows the left-behind guests also without a previous key (ROADMAP row 32)", async () => {
    const api = adminApi();
    const get = api.getSettings.bind(api);
    vi.spyOn(api, "getSettings").mockImplementation(async () => {
      const s = await get();
      if (s.repair_key)
        s.repair_key = { ...s.repair_key, previous: null, left_behind: ["wiki (VMID 150)"] };
      return s;
    });
    show(General, { api });
    await until(() => document.querySelector("#key-left-behind"));
    expect(document.querySelector("#key-previous")).toBeNull();
    expect(text(el("#key-left-behind"))).toContain("Left behind (1): wiki (VMID 150).");
  });

  it("shows no left-behind line when the list is empty", async () => {
    show(General, { api: adminApi() });
    await until(() => document.querySelector("#general-key"));
    expect(document.querySelector("#key-left-behind")).toBeNull();
  });
});

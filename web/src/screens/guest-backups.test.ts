// @vitest-environment happy-dom
// T-223 (ARCHITECTURE §20, D-405 … D-411, D-417): the backups tab of a
// Target: storage and encryption, the token's own power, back up now, the
// volumes by kind, the delete with confirm and the restore wizard.
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { GuestBackupList } from "../lib/client";
import { createMockApi } from "../lib/mock/mock";
import { me } from "../lib/session.svelte";
import TargetBackups from "./TargetBackups.svelte";
import { button, el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(() => {
  reset();
  me.session = null;
});
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const ADMIN = { principal: "user:admin", tier: "admin" } as never;
const open = async (api = adminApi(), name = "db1", session: unknown = ADMIN) => {
  me.session = session as typeof me.session;
  show(TargetBackups, { api, name });
  await until(() => document.querySelector("#backup-items"));
  return api;
};
const withStorage = (api: ReturnType<typeof adminApi>, storage: GuestBackupList["storage"]) => {
  const list = api.listGuestBackups.bind(api);
  vi.spyOn(api, "listGuestBackups").mockImplementation(async (n) => ({
    ...(await list(n)),
    storage,
  }));
  return api;
};

describe("backups tab", () => {
  it("shows the clear-text warning and the token's own power (M3 of #321)", async () => {
    await open();
    expect(text(el("#backup-storage"))).toContain("not encrypted");
    expect(text(el("#backup-encryption"))).toContain("These backups are not encrypted.");
    expect(text(el("#backup-token-power"))).toContain(
      "The Proxmox API token of Plumb can itself back up, restore over and delete the backups of every guest in the pool.",
    );
    expect(text(el("#schedule-text"))).toContain("Every day at 03:00 UTC");
    expect(text(el("#schedule-text"))).toContain('at most 2 made by "Back up now"');
  });

  it("an encrypted storage has no warning; a refused one says why", async () => {
    await open(withStorage(adminApi(), { id: "pbs", state: "pbs-key", free_pct: 40 }));
    expect(text(el("#backup-storage"))).toContain("encrypted");
    expect(el("#backup-encryption").classList.contains("warn")).toBe(false);
    reset();
    await open(withStorage(adminApi(), { id: "nfs", state: "clear-refused", free_pct: 5 }));
    expect(text(el("#backup-encryption"))).toContain("Every backup is refused.");
    expect(document.querySelector("#backup-low-space")).not.toBeNull();
    // Review L1 of #338: no job on a refused storage, with the reason.
    expect(el<HTMLButtonElement>("#backup-now").disabled).toBe(true);
    expect(el("#backup-now").getAttribute("aria-describedby")).toBe("backup-now-off");
    expect(text(el("#backup-now-off"))).toContain("does not encrypt");
  });

  it("back up now is on for a storage in clear that the install allowed", async () => {
    await open();
    expect(el<HTMLButtonElement>("#backup-now").disabled).toBe(false);
    expect(document.querySelector("#backup-now-off")).toBeNull();
  });

  it("no delete button without a known session, nor for an admin token (review of #338)", async () => {
    await open(adminApi(), "db1", null);
    expect(document.querySelector("#delete-3f2a9c0d1e4b5a67")).toBeNull();
    expect(document.querySelector("#delete-9b1c2d3e4f5a6b7c")).toBeNull();
    expect(document.querySelector("#backup-delete-role")).not.toBeNull();
    reset();
    await open(adminApi(), "db1", { principal: "token:tok_ci", tier: "admin" });
    expect(document.querySelector("#delete-3f2a9c0d1e4b5a67")).toBeNull();
  });

  it("warns of guest disk content and of rights beyond the install (M1/M2 of #330)", async () => {
    const api = adminApi();
    const get = api.getSettings.bind(api);
    vi.spyOn(api, "getSettings").mockImplementation(async () => {
      const s = await get();
      if (s.proxmox?.guest_backup) {
        s.proxmox.acl_extra = [{ path: "/storage/old-backup", role: "PlumbGuestBackup" }];
        s.proxmox.guest_backup.disk_content = ["images", "rootdir"];
      }
      return s;
    });
    await open(api);
    await until(() => document.querySelector("#backup-acl-extra"));
    expect(text(el("#backup-disk-content"))).toContain(
      "The backup storage plumb-guest-backup also takes guest disks (images, rootdir).",
    );
    expect(text(el("#backup-disk-content"))).toContain("pvesm set plumb-guest-backup --content");
    expect(text(el("#backup-acl-extra"))).toContain("/storage/old-backup (PlumbGuestBackup)");
    expect(text(el("#backup-acl-extra"))).toContain("a repair run");
  });

  it("no such warning when the fields are missing (a controller before T-218)", async () => {
    await open();
    await new Promise((r) => setTimeout(r, 20));
    flushSync();
    expect(document.querySelector("#backup-disk-content")).toBeNull();
    expect(document.querySelector("#backup-acl-extra")).toBeNull();
  });

  it("without a storage, back up now is off", async () => {
    await open(withStorage(adminApi(), null));
    expect(document.querySelector("#backup-no-storage")).not.toBeNull();
    expect(el<HTMLButtonElement>("#backup-now").disabled).toBe(true);
    expect(el("#backup-now").getAttribute("aria-describedby")).toBe("backup-now-off");
    expect(text(el("#backup-now-off"))).toContain("no guest backup storage");
  });

  it("lists the volumes by kind; only this guest's can be restored; unmarked has no action", async () => {
    await open();
    expect(document.querySelectorAll("#backup-items table")).toHaveLength(3);
    expect(document.querySelector("#restore-3f2a9c0d1e4b5a67")).not.toBeNull();
    expect(document.querySelector("#restore-9b1c2d3e4f5a6b7c")).toBeNull();
    expect(document.querySelector("#delete-9b1c2d3e4f5a6b7c")).not.toBeNull();
    expect(document.querySelector("#delete-c0ffee00c0ffee00")).toBeNull();
    expect(text(el("#backup-after-delete"))).toContain("the backups stay in Proxmox");
  });

  it("back up now starts a job and shows it", async () => {
    const api = await open();
    const now = vi.spyOn(api, "backUpGuestNow");
    type("#backup-now-reason", "Before the update.");
    el<HTMLFormElement>("#backup-schedule form").requestSubmit();
    await until(() => /A backup runs\.|The backup started\./.test(text(el("#backup-schedule"))));
    expect(now).toHaveBeenCalledWith("db1", "Before the update.");
  });

  it("deletes one volume only after the confirm", async () => {
    const api = await open();
    const del = vi.spyOn(api, "deleteGuestBackup");
    el<HTMLButtonElement>("#delete-1a2b3c4d5e6f7a8b").click();
    flushSync();
    expect(del).not.toHaveBeenCalled();
    expect(text(el("#backup-1a2b3c4d5e6f7a8b"))).toContain("This cannot be undone.");
    await until(() => document.activeElement?.id === "delete-cancel-1a2b3c4d5e6f7a8b");
    button("Delete backup")?.click();
    await until(() => !document.querySelector("#backup-1a2b3c4d5e6f7a8b"));
    expect(del).toHaveBeenCalledWith("db1", "1a2b3c4d5e6f7a8b");
  });

  it("a person with role apply sees no delete; back up now stays", async () => {
    await open(createMockApi({ role: "apply", stepDelayMs: 0 }));
    me.session = { principal: "user:alice", tier: "apply" } as never;
    flushSync();
    expect(document.querySelector("#delete-3f2a9c0d1e4b5a67")).toBeNull();
    expect(document.querySelector("#backup-delete-role")).not.toBeNull();
    expect(document.querySelector("#backup-now")).not.toBeNull();
  });

  it("a read person sees neither back up now nor restore", async () => {
    await open(createMockApi({ role: "read", stepDelayMs: 0 }));
    me.session = { principal: "user:bob", tier: "read" } as never;
    flushSync();
    expect(document.querySelector("#backup-now")).toBeNull();
    expect(document.querySelector("#restore-3f2a9c0d1e4b5a67")).toBeNull();
  });
});

describe("restore wizard", () => {
  it("states the human-only rule and what is lost, needs the confirm and a reason", async () => {
    const api = await open();
    const restore = vi.spyOn(api, "restoreGuest");
    el<HTMLButtonElement>("#restore-3f2a9c0d1e4b5a67").click();
    await until(() => document.querySelector("#restore"));
    await until(() => document.activeElement?.id === "restore-title");
    expect(text(el("#restore-human-only"))).toContain("Only a person can approve a restore.");
    expect(text(el("#restore-lost"))).toContain("is lost");
    expect(text(el("#restore-lost"))).toContain("removed later work again");
    expect(text(el("#restore-steps"))).toContain("revokes the agent credential");
    expect(text(el("#restore-steps"))).toContain("re-enrollment");
    expect(text(el("#restore-gone"))).toContain("then adopt the guest");
    el<HTMLFormElement>("#restore").requestSubmit();
    flushSync();
    expect(restore).not.toHaveBeenCalled();
    expect(el("#restore-ack").getAttribute("aria-invalid")).toBe("true");
    expect(document.querySelector("#restore-reason-error")).not.toBeNull();
    el<HTMLInputElement>("#restore-ack").click();
    type("#restore-reason", "The update broke the database.");
    el<HTMLFormElement>("#restore").requestSubmit();
    const id = await planOpened();
    expect(restore).toHaveBeenCalledWith("db1", {
      backup: "3f2a9c0d1e4b5a67",
      reason: "The update broke the database.",
    });
    const p = await api.getPlan(id);
    expect([p.origin, p.risk, p.human_only]).toEqual(["restore", "destructive", ["guest-restore"]]);
  });

  it("says that Proxmox empties the guest first and that Plumb takes a safety backup (D-420)", async () => {
    await open();
    el<HTMLButtonElement>("#restore-3f2a9c0d1e4b5a67").click();
    await until(() => document.querySelector("#restore"));
    expect(text(el("#restore-disks"))).toContain("the guest is left empty");
    expect(text(el("#restore-safety"))).toContain("Plumb takes a safety backup");
    expect(text(el("#restore-safety"))).toContain("nothing is changed");
    expect(text(el("#restore-lost"))).toContain("so you can undo the restore");
    expect(text(el("#restore-lost"))).toContain("After a restore that worked, it replaces");
    expect(el<HTMLInputElement>("#restore-skip-safety").checked).toBe(false);
  });

  it("the skip tick changes the step and is sent; the server refuses it for a guest with a disk", async () => {
    const api = await open();
    const restore = vi.spyOn(api, "restoreGuest");
    el<HTMLButtonElement>("#restore-3f2a9c0d1e4b5a67").click();
    await until(() => document.querySelector("#restore"));
    el<HTMLInputElement>("#restore-skip-safety").click();
    flushSync();
    expect(text(el("#restore-safety"))).toContain("No safety backup first");
    expect(text(el("#restore-lost"))).not.toContain("undo the restore");
    el<HTMLInputElement>("#restore-ack").click();
    type("#restore-reason", "No disk.");
    el<HTMLFormElement>("#restore").requestSubmit();
    await until(() => text().includes("only for a broken guest"));
    expect(restore).toHaveBeenCalledWith("db1", {
      backup: "3f2a9c0d1e4b5a67",
      reason: "No disk.",
      skip_safety_backup: true,
    });
  });

  it("no skip tick for a token (human-only)", async () => {
    await open(adminApi(), "db1", { principal: "token:ci", tier: "admin" });
    el<HTMLButtonElement>("#restore-3f2a9c0d1e4b5a67").click();
    await until(() => document.querySelector("#restore"));
    expect(document.querySelector("#restore-skip-safety")).toBeNull();
  });

  it("after a failed restore: the banner, no tick, the safety backup stays (kept)", async () => {
    const api = await open(adminApi(), "cache1");
    const restore = vi.spyOn(api, "restoreGuest");
    await until(() => document.querySelector("#restore-failed"));
    expect(text(el("#restore-failed"))).toContain("The last restore of this guest failed.");
    expect(text(el("#restore-failed"))).toContain("backup 7e6d5c4b3a291807");
    expect(text(el("#backup-items"))).toContain("safety backup before a restore");
    el<HTMLButtonElement>("#restore-2c3d4e5f60718293").click();
    await until(() => document.querySelector("#restore"));
    expect(document.querySelector("#restore-skip-safety")).toBeNull();
    expect(text(el("#restore-safety"))).toContain("No new safety backup");
    el<HTMLInputElement>("#restore-ack").click();
    type("#restore-reason", "Try the nightly one.");
    el<HTMLFormElement>("#restore").requestSubmit();
    const id = await planOpened();
    expect(restore.mock.calls[0]?.[1]).not.toHaveProperty("skip_safety_backup");
    const p = await api.getPlan(id);
    const ch = p.targets[0]?.resources[0]?.changes ?? [];
    expect(ch.map((c) => [c.attr, "to" in c ? c.to : null])).toContainEqual([
      "safety_backup",
      "kept: the last restore failed; the safety backup from before it stays",
    ]);
  });

  it("does not promise a safety backup when the issues cannot be read (review L2 of #350)", async () => {
    const api = adminApi();
    vi.spyOn(api, "listIssues").mockRejectedValue(new Error("down"));
    await open(api);
    el<HTMLButtonElement>("#restore-3f2a9c0d1e4b5a67").click();
    await until(() => document.querySelector("#restore"));
    expect(text(el("#restore-safety"))).toContain("Plumb decides");
    expect(text(el("#restore-safety"))).toContain("of the Plan says which");
    expect(text(el("#restore-safety"))).not.toContain(
      "Plumb takes a safety backup of the guest as it is now",
    );
    expect(text(el("#restore-lost"))).not.toContain("undo the restore");
    reset();
    await open();
    el<HTMLButtonElement>("#restore-3f2a9c0d1e4b5a67").click();
    await until(() => document.querySelector("#restore"));
    expect(document.querySelector("#restore-issues-unknown")).toBeNull();
  });

  it("opens the restore of the backup that the Issue names (?restore=)", async () => {
    me.session = ADMIN as typeof me.session;
    show(TargetBackups, { api: adminApi(), name: "cache1", restore: "7e6d5c4b3a291807" });
    await until(() => document.querySelector("#restore"));
    expect(text(el("#restore-title"))).toContain("Restore the backup of");
    reset();
    me.session = ADMIN as typeof me.session;
    show(TargetBackups, { api: adminApi(), name: "cache1", restore: "0000000000000000" });
    await until(() => document.querySelector("#restore-missing"));
    expect(document.querySelector("#restore")).toBeNull();
  });

  it("an adopted Target gets the install command, not a re-enrollment", async () => {
    const api = adminApi();
    const t = await api.getTarget("db1");
    t.document.spec.adopted = { vmid: 9002 } as never;
    vi.spyOn(api, "getTarget").mockResolvedValue(t);
    await open(api);
    el<HTMLButtonElement>("#restore-3f2a9c0d1e4b5a67").click();
    await until(() => document.querySelector("#restore"));
    expect(text(el("#restore-steps"))).toContain("install command");
  });
});

// @vitest-environment happy-dom
// T-118 review M1: the Issue screen with the action sets that the server
// sends (internal/api/views.go issueView, internal/api/raw.go): a firewall
// Issue is never accepted (D-295), so it has no raw offer; guest:missing and
// agent:silent only take the ignore actions (D-303) and show the restore hint.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import { me } from "../lib/session.svelte";
import type { Issue, IssueAction } from "../lib/types";
import IssueView from "./IssueView.svelte";
import { button, el, reset, show, text, until } from "./testkit";

afterEach(() => {
  reset();
  me.session = null;
});
const ADMIN_PERSON = { principal: "user:admin", tier: "admin" } as never;

/** An open Issue as the server sends it for `resource`. */
async function serverIssue(resource: string, server: Partial<Issue>) {
  const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
  const base = await api.getIssue(7);
  vi.spyOn(api, "getIssue").mockResolvedValue({
    ...base,
    target: "web1",
    resource,
    status: "open",
    plan_id: null,
    resolution: null,
    ...server,
  });
  show(IssueView, { api, id: 7 });
  await until(() => text().includes(resource));
  return api;
}

const accept = () =>
  button("Accept as raw for this Target") ?? button("Accept: keep the guest value");

describe("Issue actions from the server", () => {
  it("guest:config with a NIC change: revert, no accept, the reason (D-418)", async () => {
    await serverIssue("guest:config", {
      changes: [
        {
          attr: "nic1",
          from: "bridge=vmbr2,tag=30,mac=02:aa:bb:cc:dd:01",
          to: "bridge=vmbr2,tag=20,ip=10.20.0.20/24,mac=02:aa:bb:cc:dd:01",
          risk: "disruptive",
        },
        // A NIC added in Proxmox: no `to` (T-219).
        { attr: "nic3", from: "bridge=vmbr9,mac=02:aa:bb:cc:dd:03", risk: "disruptive" },
        { attr: "cores", from: "4", to: "2", risk: "disruptive" },
      ],
      actions: ["revert", "ignore_once", "ignore_rule"],
      raw: false,
    });
    expect(accept()).toBeUndefined();
    expect(button("Revert: apply the desired state")).toBeDefined();
    expect(text(el("#accept-nic"))).toContain(
      "Accept is not possible for a network card (nic1, nic3). Revert it, or change guest.nics in the Target web1.",
    );
  });

  it("guest:config without a NIC change says nothing of NICs", async () => {
    await serverIssue("guest:config", {
      changes: [{ attr: "cores", from: "4", to: "2", risk: "disruptive" }],
      actions: ["accept", "revert", "ignore_once", "ignore_rule"],
      raw: false,
    });
    expect(document.querySelector("#accept-nic")).toBeNull();
  });

  it("an attr that only starts with nic is not a NIC drift", async () => {
    await serverIssue("guest:config", {
      changes: [{ attr: "nic1.bridge", from: "a", to: "b", risk: "disruptive" }],
      actions: ["revert", "ignore_once", "ignore_rule"],
      raw: false,
    });
    expect(document.querySelector("#accept-nic")).toBeNull();
  });

  it("firewall:main: revert, no accept and no raw offer", async () => {
    await serverIssue("firewall:main", {
      changes: [{ attr: "tcp_ports", from: "22,8080", to: "22", risk: "disruptive" }],
      actions: ["revert", "ignore_once", "ignore_rule"],
      raw: false,
      source: { block: "firewall", use: "firewall" },
    });
    await until(() => button("Revert: apply the desired state"));
    expect(accept()).toBeFalsy();
    expect(document.querySelector("#raw-help")).toBeNull();
    expect(document.querySelector("#restore-hint")).toBeNull();
    expect(button("Ignore once")).toBeTruthy();
  });

  it("guest:missing: only the ignore actions, and the restore hint", async () => {
    await serverIssue("guest:missing", {
      changes: [],
      actions: ["ignore_once", "ignore_rule"],
      raw: false,
      source: null,
    });
    await until(() => document.querySelector("#restore-hint"));
    expect(text(el("#restore-hint"))).toContain("the guest of this Target is gone from Proxmox");
    expect(text(el("#restore-hint"))).toContain("Plumb never makes it again by itself");
    expect(button("Revert: apply the desired state")).toBeFalsy();
    expect(accept()).toBeFalsy();
    expect(document.querySelector("#raw-help")).toBeNull();
    expect(button("Ignore once")).toBeTruthy();
  });

  const NOTE =
    "Ignore only after the guest has a disk again (a restore, or a repair in Proxmox): Plumb refuses it before.";
  const failedIssue = (way: Issue["restore_way_back"]) => ({
    risk: "destructive" as const,
    // The prose of the change is not read any more (L1 of #350): the field is.
    changes: [
      {
        attr: "restore",
        from: "failed: the guest may have no disk",
        to: "restore the safety backup",
        risk: "destructive" as const,
      },
    ],
    actions: ["ignore_once"] as IssueAction[],
    actions_blocked_by: null,
    actions_note: NOTE,
    restore_way_back: way,
    raw: false,
    source: null,
  });

  it("guest:restore-failed: the way back from restore_way_back opens that restore (T-234)", async () => {
    me.session = ADMIN_PERSON;
    await serverIssue(
      "guest:restore-failed",
      failedIssue({ backup_id: "7e6d5c4b3a291807", time: "2026-11-15T09:20:31Z" }),
    );
    await until(() => document.querySelector("#restore-hint"));
    expect(text(el("#restore-hint"))).toContain("the guest may have no disk now");
    expect(text(el("#restore-hint"))).toContain("backup 7e6d5c4b3a291807");
    expect(text(el("#restore-failed-found"))).toContain("when the restore failed");
    expect(text()).not.toContain("Risk of a revert");
    expect(text()).not.toContain("Found by");
    const links = [...el("#restore-way-back").querySelectorAll("a")];
    expect(links.map((a) => a.getAttribute("href"))).toEqual([
      "#/targets/web1/backups?restore=7e6d5c4b3a291807",
      "#/targets/web1/backups",
    ]);
    expect(button("Revert: apply the desired state")).toBeFalsy();
    expect(accept()).toBeFalsy();
    expect(document.querySelector("#ignore-rule")).toBeNull();
    expect(text()).not.toContain("Ignore by rule");
  });

  it("guest:restore-failed: the ignore confirm shows Plumb's note; an admin person only (M1 of #352)", async () => {
    me.session = ADMIN_PERSON;
    const api = await serverIssue("guest:restore-failed", failedIssue(null));
    const ignore = vi.spyOn(api, "ignoreOnce");
    button("Ignore once")?.click();
    await until(() => document.querySelector("#ignore-q"));
    expect(text(el("#ignore-q"))).toContain("End the Issue of the failed restore?");
    expect(text(el("#ignore-q"))).toContain(NOTE);
    expect(ignore).not.toHaveBeenCalled();
    reset();
    me.session = { principal: "token:ci", tier: "admin" } as never;
    await serverIssue("guest:restore-failed", failedIssue(null));
    expect(button("Ignore once")).toBeFalsy();
    expect(text(el("#ignore-admin-only"))).toContain("Only a person with the role admin");
    reset();
    me.session = { principal: "user:alice", tier: "apply" } as never;
    await serverIssue("guest:restore-failed", failedIssue(null));
    expect(button("Ignore once")).toBeFalsy();
  });

  it("guest:restore-failed: the end while the guest has no disk shows the server's text (409 restore-not-ended)", async () => {
    me.session = ADMIN_PERSON;
    const api = createMockApi({ signedIn: true, stepDelayMs: 0, guestsWithoutDisk: ["cache1"] });
    const issue = (await api.listIssues({ target: "cache1", status: "open" })).find(
      (i) => i.resource === "guest:restore-failed",
    );
    if (!issue) throw new Error("the cache1 seed is missing");
    show(IssueView, { api, id: issue.id });
    await until(() => button("Ignore once"));
    button("Ignore once")?.click();
    await until(() => document.querySelector("#ignore-confirm"));
    el<HTMLButtonElement>("#ignore-confirm").click();
    await until(() => text().includes("restore a backup first"));
    expect(text()).toContain(
      "the guest of the Target has no disk: restore a backup first, or bring the guest back in Proxmox; then end this Issue",
    );
    expect(text()).not.toContain("Ended. Plumb compares the guest again");
    expect((await api.getIssue(issue.id)).status).toBe("open");
  });

  it("guest:restore-failed without a safety backup: only the backups link", async () => {
    await serverIssue("guest:restore-failed", failedIssue(null));
    await until(() => document.querySelector("#restore-way-back"));
    expect(text(el("#restore-hint"))).toContain("restore another backup");
    expect(el("#restore-way-back").querySelectorAll("a")).toHaveLength(1);
  });

  it("another Issue of a Target whose restore failed: the note and the link, no buttons (T-234)", async () => {
    me.session = ADMIN_PERSON;
    await serverIssue("guest:config", {
      changes: [{ attr: "memory", from: 4096, to: 2048, risk: "disruptive" }],
      actions: [],
      actions_blocked_by: 11,
      actions_note:
        "The last restore of web1 failed, and the guest may have no disk: Issue 11 names the way back.",
      raw: false,
      source: null,
    });
    await until(() => document.querySelector("#actions-blocked"));
    expect(text(el("#actions-blocked"))).toContain("Issue 11 names the way back");
    expect(el("#actions-blocked a").getAttribute("href")).toBe("#/issues/11");
    expect(document.querySelector("#resolve-title")).toBeNull();
    expect(button("Revert: apply the desired state")).toBeFalsy();
    expect(button("Ignore once")).toBeFalsy();
  });

  it("agent:silent: the restore hint links to the re-enrollment", async () => {
    await serverIssue("agent:silent", {
      changes: [],
      actions: ["ignore_once", "ignore_rule"],
      raw: false,
      source: null,
    });
    await until(() => document.querySelector("#restore-hint"));
    expect(text(el("#restore-hint"))).toContain("did not say hello within 10 minutes");
    const link = el("#restore-hint a") as HTMLAnchorElement;
    expect(text(link)).toBe("Re-enroll the agent");
    expect(link.getAttribute("href")).toBe("#/targets/web1/edit");
    expect(accept()).toBeFalsy();
    expect(button("Revert: apply the desired state")).toBeFalsy();
  });
});

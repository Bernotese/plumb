// @vitest-environment happy-dom
// T-118 review M1: the Issue screen with the action sets that the server
// sends (internal/api/views.go issueView, internal/api/raw.go): a firewall
// Issue is never accepted (D-295), so it has no raw offer; guest:missing and
// agent:silent only take the ignore actions (D-303) and show the restore hint.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { Issue } from "../lib/types";
import IssueView from "./IssueView.svelte";
import { button, el, reset, show, text, until } from "./testkit";

afterEach(reset);

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

  it("guest:restore-failed: ignore only, the way back opens the restore of the named backup (D-420)", async () => {
    await serverIssue("guest:restore-failed", {
      risk: "destructive",
      changes: [
        {
          attr: "restore",
          from: "failed: the guest may have no disk",
          to: "restore the safety backup backup 7e6d5c4b3a291807 of 2026-11-15T09:20:31Z",
          risk: "destructive",
        },
      ],
      actions: ["ignore_once", "ignore_rule"],
      raw: false,
      source: null,
    });
    await until(() => document.querySelector("#restore-hint"));
    expect(text(el("#restore-hint"))).toContain("the guest may have no disk now");
    expect(text(el("#restore-hint"))).toContain("backup 7e6d5c4b3a291807");
    expect(text(el("#restore-hint"))).not.toContain("backup backup");
    const links = [...el("#restore-way-back").querySelectorAll("a")];
    expect(links.map((a) => a.getAttribute("href"))).toEqual([
      "#/targets/web1/backups?restore=7e6d5c4b3a291807",
      "#/targets/web1/backups",
    ]);
    expect(button("Revert: apply the desired state")).toBeFalsy();
    expect(accept()).toBeFalsy();
    expect(button("Ignore once")).toBeTruthy();
  });

  it("guest:restore-failed without a safety backup: only the backups link", async () => {
    await serverIssue("guest:restore-failed", {
      changes: [
        { attr: "restore", from: "failed", to: "restore another backup", risk: "destructive" },
      ],
      actions: ["ignore_once", "ignore_rule"],
      raw: false,
      source: null,
    });
    await until(() => document.querySelector("#restore-way-back"));
    expect(text(el("#restore-hint"))).toContain("restore another backup");
    expect(el("#restore-way-back").querySelectorAll("a")).toHaveLength(1);
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

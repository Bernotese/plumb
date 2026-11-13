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

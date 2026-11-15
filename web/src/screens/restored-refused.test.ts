// @vitest-environment happy-dom
// D-439 (T-241): a restore that worked but whose re-enroll Plan was
// refused. The Issue view shows the state and offers the re-enroll Plan,
// the only Plan that the gate lets through; a read session cannot.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import { me } from "../lib/session.svelte";
import type { Issue } from "../lib/types";
import IssueView from "./IssueView.svelte";
import { button, el, reset, show, text, until } from "./testkit";

afterEach(() => {
  reset();
  me.session = null;
});

const NOTE =
  "restored; the agent is not enrolled: the re-enroll Plan was refused: the container has additions that were made outside Plumb. Way out: remove the addition in Proxmox, then POST /targets/web1/reenroll. Only a re-enroll Plan of this Target, a new restore or a delete is allowed until then.";

async function restoredIssue(tier: string) {
  me.session = { principal: "user:alice", tier } as never;
  const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
  const base = await api.getIssue(7);
  vi.spyOn(api, "getIssue").mockResolvedValue({
    ...base,
    target: "web1",
    resource: "guest:restore-failed",
    status: "open",
    plan_id: null,
    resolution: null,
    changes: [
      {
        attr: "restore",
        from: "restored; the agent is not enrolled: the re-enroll Plan was refused: the container has additions that were made outside Plumb",
        to: "remove the addition in Proxmox, then POST /targets/web1/reenroll",
        risk: "disruptive",
      },
      { attr: "restore_run", to: 12, risk: "read" },
    ],
    actions: ["ignore_once"],
    actions_note: NOTE,
  } as Issue);
  show(IssueView, { api, id: 7 });
  await until(() => text().includes("guest:restore-failed"));
  return api;
}

describe("restored, re-enroll refused (D-439)", () => {
  it("shows the state and proposes the re-enroll", async () => {
    const api = await restoredIssue("plan");
    expect(text(el("#restored-refused"))).toContain("Restored, the agent is not enrolled");
    expect(text(el("#restored-refused"))).toContain("remove the addition in Proxmox");
    const spy = vi.spyOn(api, "reenrollTarget");
    const propose = button("Propose the re-enroll of web1");
    expect(propose).toBeDefined();
    propose?.click();
    await until(() => spy.mock.calls.length > 0);
    expect(spy.mock.calls[0]?.[0]).toBe("web1");
  });

  it("a read session sees the state but no button", async () => {
    await restoredIssue("read");
    expect(text(el("#restored-refused"))).toContain("the agent is not enrolled");
    expect(button("Propose the re-enroll of web1")).toBeUndefined();
  });

  it("a failed restore shows no re-enroll", async () => {
    me.session = { principal: "user:alice", tier: "admin" } as never;
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const base = await api.getIssue(7);
    vi.spyOn(api, "getIssue").mockResolvedValue({
      ...base,
      resource: "guest:restore-failed",
      status: "open",
      changes: [
        {
          attr: "restore",
          from: "failed: the guest may have no disk",
          to: "restore the safety backup x",
          risk: "destructive",
        },
      ],
      actions: ["ignore_once"],
    } as Issue);
    show(IssueView, { api, id: 7 });
    await until(() => text().includes("guest:restore-failed"));
    expect(document.querySelector("#restored-refused")).toBeNull();
  });
});

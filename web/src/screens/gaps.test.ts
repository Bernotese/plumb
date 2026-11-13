// @vitest-environment happy-dom
// Screen tests of T-153 on the mock: web gaps that need no new API (D-338).
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import BlockView from "./BlockView.svelte";
import ImageView from "./ImageView.svelte";
import IssueView from "./IssueView.svelte";
import Issues from "./Issues.svelte";
import Plans from "./Plans.svelte";
import PlanView from "./PlanView.svelte";
import RevisionView from "./RevisionView.svelte";
import Runs from "./Runs.svelte";
import TargetForm from "./TargetForm.svelte";
import Targets from "./Targets.svelte";
import { button, el, planOpened, reset, rows, show, text, type, until } from "./testkit";

afterEach(reset);

function must<T>(v: T | null | undefined): T {
  if (v === null || v === undefined) throw new Error("missing element");
  return v;
}

describe("delete by a proposal", () => {
  it("deletes an unused Block through a plan, never directly", async () => {
    const api = createMockApi({ signedIn: true });
    const create = vi.spyOn(api, "createPlan");
    show(BlockView, { api, name: "nginx-site" });
    (await until(() => button("Delete block…"))).click();
    await until(() => document.querySelector("#delete-block-reason"));
    await until(() => document.activeElement?.id === "delete-block-reason");
    // An empty reason is a field error, not a call.
    el("form").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await until(() => document.querySelector("#delete-block-reason-error"));
    expect(create).not.toHaveBeenCalled();
    type("#delete-block-reason", "Not used any more.");
    must(button("Make delete plan")).click();
    const id = await planOpened();
    expect(create).toHaveBeenCalledWith({
      reason: "Not used any more.",
      delete: [{ kind: "Block", name: "nginx-site" }],
    });
    const plan = await api.getPlan(id);
    expect(plan.status).toBe("pending");
  });

  it("offers no delete for a Block that a Target uses", async () => {
    const api = createMockApi({ signedIn: true });
    show(BlockView, { api, name: "motd" });
    await until(() => document.querySelector("#delete-block-blocked"));
    expect(text()).toContain("Remove it from them first.");
    expect(text()).not.toContain("nothing uses it");
    expect(button("Delete block…")).toBeUndefined();
  });

  it("deletes a Target: the plan deletes the guest with destructive risk", async () => {
    const api = createMockApi({ signedIn: true });
    show(TargetForm, { api, name: "cache1" });
    (await until(() => button("Delete target…"))).click();
    await until(() => document.querySelector("#delete-target-reason"));
    expect(text()).toContain("and its disks in Proxmox");
    type("#delete-target-reason", "Cache moves to db1.");
    must(button("Make delete plan")).click();
    const plan = await api.getPlan(await planOpened());
    expect(plan.risk).toBe("destructive");
    expect(plan.targets).toEqual([
      expect.objectContaining({
        target: "cache1",
        resources: [expect.objectContaining({ resource: "guest:config", action: "delete" })],
      }),
    ]);
  });

  it("shows a server refusal at the delete form", async () => {
    const api = createMockApi({ signedIn: true });
    vi.spyOn(api, "createPlan").mockRejectedValue(new Error("Block is used by web9."));
    show(BlockView, { api, name: "net-tuning" });
    (await until(() => button("Delete block…"))).click();
    await until(() => document.querySelector("#delete-block-reason"));
    type("#delete-block-reason", "Old.");
    must(button("Make delete plan")).click();
    await until(() => text().includes("Block is used by web9."));
    expect(location.hash).toBe("");
  });

  it("deletes an unused Image; an Image in use names its Targets", async () => {
    const api = createMockApi({ signedIn: true });
    show(ImageView, { api, name: "ubuntu-26.04" });
    const blocked = await until(() => document.querySelector("#delete-image-blocked"));
    expect(blocked.textContent).toContain("web1");
    reset();
    const create = vi.spyOn(api, "createPlan");
    show(ImageView, { api, name: "debian-tools" });
    (await until(() => button("Delete image…"))).click();
    await until(() => document.querySelector("#delete-image-reason"));
    type("#delete-image-reason", "Not needed.");
    must(button("Make delete plan")).click();
    await planOpened();
    expect(create).toHaveBeenCalledWith({
      reason: "Not needed.",
      delete: [{ kind: "Image", name: "debian-tools" }],
    });
  });
});

describe("plan list", () => {
  it("lists pending plans and drops one after a confirm", async () => {
    const api = createMockApi({ signedIn: true });
    const discard = vi.spyOn(api, "discardPlan");
    show(Plans, { api });
    await until(() => rows().length);
    expect(rows().every((r) => r.includes("pending"))).toBe(true);
    expect(rows().some((r) => r.startsWith("Plan 14"))).toBe(true);
    must(button("Drop plan 14")).click();
    const confirm = await until(() =>
      document.querySelector<HTMLButtonElement>("#drop-confirm-14"),
    );
    await until(() => document.activeElement === confirm);
    expect(discard).not.toHaveBeenCalled();
    confirm.click();
    await until(() => !rows().some((r) => r.startsWith("Plan 14")));
    expect(discard).toHaveBeenCalledWith(14);
    expect((await api.getPlan(14)).status).toBe("stale");
  });

  it("filters by status and pages with Load older", async () => {
    const api = createMockApi({ signedIn: true });
    const list = vi.spyOn(api, "listPlans");
    show(Plans, { api, pageSize: 2 });
    await until(() => rows().length === 2);
    type("#plan-filter", "all");
    await until(() => list.mock.calls.some((c) => c[0] === undefined));
    // Page 1 of all plans: 22, 21; the older pages hold the approved and the stale plan.
    (await until(() => button("Load older plans"))).click();
    await until(() => rows().length === 4);
    (await until(() => button("Load older plans"))).click();
    await until(() => rows().some((r) => r.startsWith("Plan 11")));
    type("#plan-filter", "stale");
    await until(() => rows().length === 1 && rows()[0]?.startsWith("Plan 11"));
    expect(button("Drop plan 11")).toBeUndefined();
  });

  it("drop of a plan for an issue opens the issue again (mock)", async () => {
    const api = createMockApi({ signedIn: true });
    await api.discardPlan(15);
    expect((await api.getIssue(7)).status).toBe("open");
    await expect(api.discardPlan(15)).rejects.toThrow();
  });
});

describe("plan page drop", () => {
  it("drops the plan after a confirm and shows it stale", async () => {
    const api = createMockApi({ signedIn: true });
    const discard = vi.spyOn(api, "discardPlan");
    show(PlanView, { api, id: 14 });
    (await until(() => button("Drop plan"))).click();
    const confirm = await until(() => document.querySelector<HTMLButtonElement>("#drop-confirm"));
    await until(() => document.activeElement === confirm);
    expect(discard).not.toHaveBeenCalled();
    confirm.click();
    await until(() => text().includes("This plan is stale."));
    expect(discard).toHaveBeenCalledWith(14);
    expect(button("Approve and apply")).toBeUndefined();
    expect(el<HTMLAnchorElement>(".banner.warn a").getAttribute("href")).toBe("#/plans");
  });
});

describe("run list", () => {
  it("lists runs newest first with plan and status", async () => {
    const api = createMockApi({ signedIn: true });
    show(Runs, { api });
    await until(() => rows().length);
    expect(rows()[0]).toMatch(/^Run 19\s*succeeded/);
    expect(rows().some((r) => /^Run 17\s*failed/.test(r))).toBe(true);
    expect(el<HTMLAnchorElement>("tbody a").getAttribute("href")).toBe("#/runs/19");
  });
});

describe("issue comments", () => {
  it("adds a comment and shows it", async () => {
    const api = createMockApi({ signedIn: true });
    const add = vi.spyOn(api, "commentIssue");
    show(IssueView, { api, id: 9 });
    await until(() => button("Add comment"));
    must(button("Add comment")).click();
    await until(() => document.querySelector("#comment-error"));
    expect(add).not.toHaveBeenCalled();
    type("#comment-body", "Checked with the owner.\nRevert next week.");
    must(button("Add comment")).click();
    await until(() => text().includes("Comment added."));
    expect(add).toHaveBeenCalledWith(9, "Checked with the owner.\nRevert next week.");
    expect(text()).toContain("Checked with the owner.");
    expect(el<HTMLTextAreaElement>("#comment-body").value).toBe("");
    expect((await api.getIssue(9)).comments.at(-1)?.author).toBe("user:admin");
  });
});

describe("issue filter by target", () => {
  it("lists only the issues of one target and links from the target list", async () => {
    const api = createMockApi({ signedIn: true });
    const list = vi.spyOn(api, "listIssues");
    show(Issues, { api, target: "db1" });
    await until(() => rows().length);
    expect(list).toHaveBeenCalledWith({ target: "db1" });
    expect(rows().every((r) => r.includes("db1"))).toBe(true);
    await until(() => document.querySelectorAll("#issue-target option").length > 2);
    type("#issue-target", "web1");
    expect(location.hash).toBe("#/issues?target=web1");
    type("#issue-target", "");
    expect(location.hash).toBe("#/issues");
    reset();
    show(Targets, { api });
    const link = await until(() =>
      [...document.querySelectorAll("a")].find((a) => /open issue/.test(a.textContent ?? "")),
    );
    expect(link.getAttribute("href")).toMatch(/^#\/issues\?target=/);
  });
});

describe("compare two revisions", () => {
  it("compares with any base revision", async () => {
    const api = createMockApi({ signedIn: true });
    const diff = vi.spyOn(api, "diffRevision");
    show(RevisionView, { api, id: 7, from: 5 });
    await until(() => text().includes("Changes from revision"));
    expect(diff).toHaveBeenCalledWith(7, 5);
    expect(text()).toContain("With the revision before");
    type("#compare-from", "abc");
    must(el("#compare-from").closest("form")).dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    );
    await until(() => document.querySelector("#compare-error"));
    type("#compare-from", "7");
    must(el("#compare-from").closest("form")).dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    );
    await until(() => text().includes("Pick another revision"));
    type("#compare-from", "6");
    must(button("Compare")).click();
    await until(() => location.hash === "#/revisions/7?from=6");
  });
});

// @vitest-environment happy-dom
// T-154: role-aware buttons (D-329, D-342). Comfort only: the server checks
// every call (T-147). A person with role read sees no write button.
import { afterEach, describe, expect, it } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import { me, type Role } from "../lib/session.svelte";
import BlockView from "./BlockView.svelte";
import ImageView from "./ImageView.svelte";
import Images from "./Images.svelte";
import IssueView from "./IssueView.svelte";
import PlanView from "./PlanView.svelte";
import Plans from "./Plans.svelte";
import RevisionView from "./RevisionView.svelte";
import Rules from "./Rules.svelte";
import TargetForm from "./TargetForm.svelte";
import Targets from "./Targets.svelte";
import { button, reset, rows, show, text, until } from "./testkit";

afterEach(() => {
  reset();
  me.session = null;
});

async function as(role: Role) {
  const api = createMockApi({ role, stepDelayMs: 0 });
  me.session = await api.session();
  return api;
}

const writeButtons = () =>
  [...document.querySelectorAll("button")]
    .map((b) => b.textContent?.replace(/\s+/g, " ").trim() ?? "")
    .filter(
      (t) =>
        !/^(Compare|Show difference|Hide difference|Copy.*|Load older.*|Show \d+ unchanged lines?)$/u.test(
          t,
        ),
    );

describe("role read: no write button", () => {
  it.each([
    ["Targets", Targets, {}],
    ["Images", Images, {}],
    ["Plans", Plans, {}],
    ["Rules", Rules, {}],
    ["BlockView", BlockView, { name: "nginx-site" }],
    ["ImageView", ImageView, { name: "ubuntu-26.04" }],
    ["RevisionView", RevisionView, { id: 7 }],
    ["TargetForm", TargetForm, { name: "web1" }],
  ] as const)("%s", async (_n, screen, props) => {
    const api = await as("read");
    show(screen, { api, ...props });
    await until(() => document.querySelector("#page-title") && !text().includes("Loading…"));
    await new Promise((r) => setTimeout(r, 50));
    expect(writeButtons()).toEqual(
      screen === TargetForm
        ? expect.not.arrayContaining(["Show the plan", "Plan the re-enrollment", "Delete target…"])
        : [],
    );
    expect(document.querySelector('a.btn.primary[href$="/new"], a.btn[href$="/edit"]')).toBeNull();
  });

  it("the plan page has no approve and no drop", async () => {
    const api = await as("read");
    show(PlanView, { api, id: 14 });
    await until(() => document.querySelector("#approve-role"));
    expect(text()).toContain("Your role is read. Approving needs the role apply or admin.");
    expect(button("Approve and apply")).toBeUndefined();
    expect(button("Drop plan")).toBeUndefined();
  });

  it("the issue page has no resolve and no comment form", async () => {
    const api = await as("read");
    show(IssueView, { api, id: 9 });
    await until(() => document.querySelector("#resolve-role"));
    expect(button(/Revert/)).toBeUndefined();
    expect(button("Add comment")).toBeUndefined();
    expect(button("Ignore once")).toBeUndefined();
  });

  it("the target list still links to each target", async () => {
    const api = await as("read");
    show(Targets, { api });
    await until(() => rows().length);
    expect(document.querySelector('a[href="#/targets/web1/edit"]')).not.toBeNull();
  });
});

describe("role plan", () => {
  it("proposes and drops, but does not approve", async () => {
    const api = await as("plan");
    show(PlanView, { api, id: 14 });
    await until(() => button("Drop plan"));
    expect(button("Approve and apply")).toBeUndefined();
    reset();
    me.session = await api.session();
    show(IssueView, { api, id: 9 });
    await until(() => button("Add comment"));
    expect(button(/Revert/)).toBeDefined();
    expect(button("Ignore once")).toBeUndefined(); // ignore_once needs apply
  });
});

describe("role apply (D-329)", () => {
  it("approves a reversible plan, not a destructive one", async () => {
    const api = await as("apply");
    show(PlanView, { api, id: 14 });
    await until(() => button("Approve and apply"));
    reset();
    me.session = await api.session();
    const p = await api.createPlan({
      reason: "Remove cache1.",
      delete: [{ kind: "Target", name: "cache1" }],
    });
    expect(p.risk).toBe("destructive");
    show(PlanView, { api, id: p.id });
    await until(() => document.querySelector("#approve-role"));
    expect(text()).toContain("A destructive plan needs the role admin.");
    expect(button("Approve and apply")).toBeUndefined();
    await expect(api.approvePlan(p.id)).rejects.toMatchObject({ problem: { status: 403 } });
  });
});

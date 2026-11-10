// @vitest-environment happy-dom
// Screen tests of T-036 on the mock: secret parameters, ignore actions, rules.
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import IssueView from "./IssueView.svelte";
import Issues from "./Issues.svelte";
import PlanView from "./PlanView.svelte";
import Rules from "./Rules.svelte";
import TargetForm from "./TargetForm.svelte";
import { button, el, planOpened, reset, rows, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

async function newTargetWithApp() {
  await until(() => document.querySelector("#add-choice"));
  type("#t-name", "app1");
  type("#add-choice", "app");
  button("Add Block")?.click();
  return until(() => document.querySelector<HTMLSelectElement>("#use-0-db_password"));
}

describe("secret parameters", () => {
  it("chooses an existing secret and saves {secret: name}", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api });
    const picker = await newTargetWithApp();
    await until(() => picker.options.length === 3);
    expect([...picker.options].map((o) => o.value)).toEqual(["", "app-db", "deploy-key"]);
    expect(text()).toContain("Plumb saves only the secret name, never the value.");

    type("#t-reason", "New app.");
    el<HTMLFormElement>("form").requestSubmit();
    flushSync();
    expect(createPlan).not.toHaveBeenCalled();
    expect(text()).toContain("Choose a secret.");

    type("#use-0-db_password", "app-db");
    el<HTMLFormElement>("form").requestSubmit();
    const id = await planOpened();
    const put = createPlan.mock.calls[0]?.[0].put?.[0] as { spec: { blocks: unknown[] } };
    expect(put.spec.blocks).toEqual([
      { use: "app", params: { db_host: "db1", db_password: { secret: "app-db" } } },
    ]);
    const plan = await api.getPlan(id);
    expect(plan.targets[0]?.secrets).toEqual([{ name: "app-db", version: 4 }]);
    expect(plan.human_only).toEqual(["secret-binding"]);
  });

  it("creates a secret in place and selects it", async () => {
    const api = adminApi();
    const put = vi.spyOn(api, "putSecret");
    show(TargetForm, { api });
    const picker = await newTargetWithApp();
    button("New secret")?.click();
    await until(() => document.activeElement?.id === "use-0-db_password-new-name");
    const value = el<HTMLInputElement>("#use-0-db_password-new-value");
    expect(value.type).toBe("password");
    expect(value.getAttribute("autocomplete")).toBe("new-password");
    type("#use-0-db_password-new-name", "app1-db");
    type("#use-0-db_password-new-value", "hunter22hunter22");
    button("Create secret")?.click();
    await until(() => picker.value === "app1-db");
    expect(put).toHaveBeenCalledWith("app1-db", "hunter22hunter22");
    expect(document.querySelector("#use-0-db_password-new-value")).toBeNull();
    expect(document.body.innerHTML).not.toContain("hunter22hunter22");
    expect(document.activeElement).toBe(picker);
  });
});

describe("PlanView", () => {
  it("names the secrets and why only a person can approve", async () => {
    const api = adminApi();
    const t = await api.getTarget("web1");
    t.document.spec.blocks = [
      ...(t.document.spec.blocks ?? []),
      { use: "app", params: { db_password: { secret: "app-db" } } },
    ];
    const plan = await api.createPlan({ reason: "Add app.", put: [t.document] });
    show(PlanView, { api, id: plan.id });
    await until(() => text().includes("Uses secrets"));
    expect(text()).toContain("Uses secrets: app-db (version 4). Values are never shown.");
    expect(text()).toContain("Only a person can approve this plan.");
    expect(text()).toContain("It gives secret app-db to web1.");
    expect(text()).toContain("changed (hidden)");
    expect(document.body.innerHTML).not.toContain("");
  });

  it("marks a change that a rule ignores", async () => {
    const api = adminApi();
    // The example rule ignore-web1-motd covers the content of file:/etc/motd on web1.
    const t = await api.getTarget("web1");
    t.document.spec.blocks = (t.document.spec.blocks ?? []).map((b) =>
      b.use === "motd" ? { use: "motd", params: { text: "New text" } } : b,
    );
    const plan = await api.createPlan({ reason: "New MOTD.", put: [t.document] });
    show(PlanView, { api, id: plan.id });
    await until(() => text().includes("A rule ignores this drift; this apply overwrites it."));
  });
});

describe("IssueView", () => {
  it("ignores once with a reason and shows the issue as ignored", async () => {
    const api = adminApi();
    const ignore = vi.spyOn(api, "ignoreOnce");
    show(IssueView, { api, id: 8 });
    await until(() => button("Ignore once"));
    type("#resolve-reason", "Known manual change.");
    button("Ignore once")?.click();
    await until(() => document.querySelector('[role="status"]'));
    expect(ignore).toHaveBeenCalledWith(8, "Known manual change.");
    expect(text()).toContain("Ignored once.");
    expect(text()).toContain("Resolved: ignored by user:admin");
    expect(button("Ignore once")).toBeUndefined();
  });

  it("shows the 403 problem text for a token session", async () => {
    const api = createMockApi({ token: true });
    show(IssueView, { api, id: 8 });
    await until(() => button("Ignore once"));
    button("Ignore once")?.click();
    const alert = await until(() => document.querySelector('[role="alert"]'));
    expect(alert.textContent).toContain("Human session required.");
    expect(alert.textContent).toContain("cannot revert this drift, so it cannot ignore it");
  });

  it("makes an ignore-rule plan with the chosen scope", async () => {
    const api = adminApi();
    const resolve = vi.spyOn(api, "resolveIssue");
    show(IssueView, { api, id: 9 });
    await until(() => button("Make ignore-rule plan"));
    el<HTMLInputElement>('input[name="rule-scope"][value="true"]').click();
    el<HTMLInputElement>('input[name="rule-attrs"][value="true"]').click();
    flushSync();
    button("Make ignore-rule plan")?.click();
    const id = await planOpened();
    expect(resolve).toHaveBeenCalledWith(9, "ignore_rule", undefined, {
      all_targets: true,
      attrs: ["content"],
    });
    const plan = await api.getPlan(id);
    expect(plan.origin).toBe("ignore_rule");
    expect(plan.human_only).toEqual(["rule-change"]);

    // Approve: the issue resolves as ignore, the target records the drift.
    await api.approvePlan(id);
    await api.idle();
    expect((await api.getIssue(9)).resolution?.action).toBe("ignore");
    const drift = (await api.getTarget("web1")).status.ignored_drift;
    expect(drift?.[0]).toMatchObject({ resource: "file:/etc/app.conf", attrs: ["content"] });
    expect((await api.listRules()).map((r) => r.document.spec.target)).toContain("*");
  });

  it("offers only the actions of the issue", async () => {
    const api = adminApi();
    const issue = await api.getIssue(9);
    expect(issue.actions).toEqual(["revert", "ignore_once", "ignore_rule"]);
    show(IssueView, { api, id: 9 });
    await until(() => button("Ignore once"));
    expect(button(/^Accept/)).toBeUndefined();
  });
});

describe("Issues and rules", () => {
  it("lists resolved issues with their resolution", async () => {
    const api = adminApi();
    await api.ignoreOnce(8);
    show(Issues, { api });
    await until(() => document.querySelector("#issue-filter"));
    type("#issue-filter", "resolved");
    await until(() => rows().some((r) => r.includes("Issue 8")));
    const row = rows().find((r) => r.includes("Issue 8")) ?? "";
    expect(row).toContain("Ignored");
    expect(row).toContain("user:admin");
    expect(el<HTMLAnchorElement>('a[href="#/issues/rules"]').textContent).toBe("Ignore rules");
  });

  it("lists rules and removes one through a plan", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(Rules, { api });
    await until(() => rows().length === 2);
    const all = rows().find((r) => r.includes("ignore-all-apt-periodic")) ?? "";
    expect(all).toContain("All targets");
    expect(all).toContain("file:/etc/apt/apt.conf.d/20auto-upgrades");
    expect(all).toContain("All");
    expect(rows().find((r) => r.includes("ignore-web1-motd"))).toContain("content");

    button("Remove ignore-web1-motd")?.click();
    await until(() => document.activeElement?.id === "remove-reason-ignore-web1-motd");
    button("Make removal plan")?.click();
    flushSync();
    expect(createPlan).not.toHaveBeenCalled(); // a reason is required
    type("#remove-reason-ignore-web1-motd", "Not needed.");
    button("Make removal plan")?.click();
    const id = await planOpened();
    expect(createPlan).toHaveBeenCalledWith({
      reason: "Not needed.",
      delete: [{ kind: "Rule", name: "ignore-web1-motd" }],
    });
    const plan = await api.getPlan(id);
    expect(plan.human_only).toEqual(["rule-change"]);
    await api.approvePlan(id);
    await api.idle();
    expect((await api.listRules()).map((r) => r.document.name)).toEqual([
      "ignore-all-apt-periodic",
    ]);
  });

  it("the target view shows drift that a rule ignores", async () => {
    const api = adminApi();
    const plan = await api.resolveIssue(9, "ignore_rule");
    await api.approvePlan(plan.id);
    await api.idle();
    show(TargetForm, { api, name: "web1" });
    await until(() => text().includes("Ignored drift"));
    expect(text()).toContain("1 difference on this guest is ignored by a rule.");
    expect(text()).toContain("file:/etc/app.conf: content · rule ignore-web1-file-etc-app-conf");
  });
});

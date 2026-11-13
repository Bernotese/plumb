// @vitest-environment happy-dom
// T-118: raw mode on Issues and Targets (D-296).
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { TargetDoc } from "../lib/types";
import IssueView from "./IssueView.svelte";
import TargetForm from "./TargetForm.svelte";
import { button, el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

describe("raw resources on the Target page", () => {
  it("lists an override with its content", async () => {
    show(TargetForm, { api: adminApi(), name: "worker1" });
    await until(() => document.querySelector("#raw-title"));
    const card = text(el('section[aria-labelledby="raw-title"]'));
    expect(card).toContain("file:/etc/motd");
    expect(card).toContain("content");
    expect(el("#raw-file-etc-motd-value").textContent).toBe(
      "Maintenance window: Sunday 02:00 UTC\n",
    );
  });

  it("edits the content as a proposal and keeps the other attributes", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api, name: "worker1" });
    await until(() => document.querySelector("#raw-title"));
    button("Edit content")?.click();
    flushSync();
    await Promise.resolve();
    expect(document.activeElement).toBe(el("#raw-file-etc-motd-content"));
    type("#raw-file-etc-motd-content", "Closed today.\n");
    [...document.querySelectorAll<HTMLButtonElement>(".raw button")]
      .find((b) => b.textContent?.trim() === "Show the plan")
      ?.click();
    flushSync();
    expect(createPlan).not.toHaveBeenCalled();
    expect(text()).toContain("Say why you make this change.");
    expect(document.activeElement).toBe(el("#raw-reason"));
    type("#raw-reason", "New motd.");
    [...document.querySelectorAll<HTMLButtonElement>(".raw button")]
      .find((b) => b.textContent?.trim() === "Show the plan")
      ?.click();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.name).toBe("worker1");
    expect(doc.spec.overrides).toEqual({
      "file:/etc/motd": { content: "Closed today.\n" },
    });
    expect(doc.spec.blocks).toEqual([{ use: "motd" }]);
  });

  it("goes back to the Block: the plan removes the override", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api, name: "worker1" });
    await until(() => document.querySelector("#raw-title"));
    type("#raw-reason", "Use the Block again.");
    button("Back to the Block")?.click();
    const id = await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.spec.overrides).toEqual({});
    const plan = await api.getPlan(id);
    expect(JSON.stringify(plan.targets)).toContain("file:/etc/motd");
  });

  it("says when a Target has no raw resources", async () => {
    show(TargetForm, { api: adminApi(), name: "db1" });
    await until(() => document.querySelector("#raw-none"));
    expect(document.querySelector("#raw-reason")).toBeNull();
  });
});

describe("raw mode on an Issue", () => {
  const openIssue = async (resource: string) => {
    const api = adminApi();
    const issue = await api.getIssue(7);
    vi.spyOn(api, "getIssue").mockResolvedValue({
      ...issue,
      resource,
      status: "open",
      plan_id: null,
      actions: ["accept", "revert"],
    });
    return api;
  };

  it("offers Accept as raw for a Block resource", async () => {
    show(IssueView, { api: await openIssue("file:/etc/motd"), id: 7 });
    await until(() => text().includes("Resolve"));
    expect(button("Accept as raw for this Target")).toBeDefined();
    expect(text(el("#raw-help"))).toContain('lists it under "Raw resources"');
  });

  it("keeps the plain Accept for the guest config", async () => {
    show(IssueView, { api: await openIssue("guest:config"), id: 7 });
    await until(() => text().includes("Resolve"));
    expect(button("Accept: keep the guest value")).toBeDefined();
    expect(document.querySelector("#raw-help")).toBeNull();
  });
});

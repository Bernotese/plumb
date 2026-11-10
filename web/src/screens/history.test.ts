// @vitest-environment happy-dom
// Screen tests of T-034 on the mock: history list, revision diff, rollback.
import { flushSync, mount, unmount } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi, type MockApi } from "../lib/mock/mock";
import type { TargetDoc } from "../lib/types";
import History from "./History.svelte";
import RevisionView from "./RevisionView.svelte";

let app: ReturnType<typeof mount> | undefined;
afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  document.body.innerHTML = "";
  location.hash = "";
});

async function until<T>(fn: () => T | undefined | null | false, ms = 2000): Promise<T> {
  const end = Date.now() + ms;
  for (;;) {
    flushSync();
    const v = fn();
    if (v) return v;
    if (Date.now() > end) throw new Error(`timeout; page: ${document.body.textContent}`);
    await new Promise((r) => setTimeout(r, 10));
  }
}

const text = () => document.body.textContent?.replace(/\s+/g, " ") ?? "";
const rows = () =>
  [...document.querySelectorAll("tbody tr")].map((r) => r.textContent?.replace(/\s+/g, " ").trim());
const button = (label: string) =>
  [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);

function show(component: typeof History | typeof RevisionView, props: Record<string, unknown>) {
  const target = document.createElement("div");
  document.body.append(target);
  app = mount(component as typeof History, { target, props: props as never });
}

/** Changes web1 through a plan and approves it: one new revision. */
async function changeWeb1(api: MockApi, cores: number, reason: string) {
  const t = await api.getTarget("web1");
  const doc = structuredClone(t.document) as TargetDoc;
  (doc.spec.guest as Record<string, unknown>)["cores"] = cores;
  const plan = await api.createPlan({ reason, put: [doc] });
  await api.approvePlan(plan.id);
  await api.idle();
}

describe("History", () => {
  it("lists revisions newest first, with author, reason and plan link, paged", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    show(History, { api, pageSize: 2 });
    await until(() => rows().length === 2);
    expect(rows()[0]).toContain("Revision 7 (head)");
    expect(rows()[0]).toContain("user:admin");
    expect(rows()[0]).toContain("Add MOTD to web1.");
    expect(rows()[1]).toContain("token:tok_ci");
    const plan = document.querySelector<HTMLAnchorElement>('tbody a[href="#/plans/12"]');
    expect(plan?.textContent).toBe("Plan 12");
    expect(document.querySelector('a[href="#/revisions/6"]')).not.toBeNull();

    button("Load older revisions")?.click();
    await until(() => rows().length === 3);
    expect(rows()[2]).toContain("Revision 5");
    expect(rows()[2]).toContain("Seeded");
    expect(button("Load older revisions")).toBeUndefined();
  });

  it("reloads when a revision is committed", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    show(History, { api });
    await until(() => rows().length === 3);
    await changeWeb1(api, 4, "More cores for web1.");
    await until(() => rows()[0]?.includes("More cores for web1."), 3000);
    expect(rows()[0]).toContain("Revision 8 (head)");
  });
});

describe("RevisionView", () => {
  it("shows added and changed documents with a YAML line diff", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    show(RevisionView, { api, id: 7 });
    await until(() => document.querySelectorAll("section.doc").length === 2);
    expect(text()).toContain(
      "Changes from revision 6 to revision 7: 1 added, 1 changed, 0 removed",
    );
    const [block, web1] = [...document.querySelectorAll("section.doc")];
    expect(block?.querySelector("h3")?.textContent).toContain("Added");
    expect(block?.querySelector("h3")?.textContent).toContain("Block motd");
    expect(web1?.querySelector("h3")?.textContent).toContain("Changed");
    expect(web1?.querySelector("h3")?.textContent).toContain("Target web1");
    // Lines are marked by a sign and by words, not only by colour.
    const added = [...(web1?.querySelectorAll("tr.add") ?? [])].map((r) => r.textContent);
    const removed = [...(web1?.querySelectorAll("tr.del") ?? [])].map((r) => r.textContent);
    expect(removed.join("|")).toContain("−Removed: ");
    expect(added.some((l) => l?.includes("+Added: ") && l.includes("- use: motd"))).toBe(true);
    expect(removed.some((l) => l?.includes("Removed: ") && l.includes("blocks: []"))).toBe(true);
  });

  it("hides unchanged lines until asked; diffs against another base", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    await changeWeb1(api, 6, "Six cores.");
    show(RevisionView, { api, id: 8 });
    const doc = await until(() => document.querySelector("section.doc"));
    expect(doc.querySelectorAll("tr.del").length).toBe(1);
    expect(doc.querySelectorAll("tr.add").length).toBe(1);
    const more = await until(() =>
      [...doc.querySelectorAll("button")].find((b) =>
        /Show \d+ unchanged lines/.test(b.textContent ?? ""),
      ),
    );
    const before = doc.querySelectorAll("tr").length;
    more.click();
    flushSync();
    expect(doc.querySelectorAll("tr").length).toBeGreaterThan(before);
    expect(doc.querySelector("tr.skip")).toBeNull();

    // Revision 6 compared with revision 7 from the other side: motd is removed.
    const back = await api.diffRevision(6, 7);
    expect(back.changes.find((c) => c.name === "motd")?.op).toBe("removed");
  });

  it("rolls back with a reason and opens the plan", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    show(RevisionView, { api, id: 6 });
    const submit = await until(() => button("Roll back to this revision"));
    const reason = document.querySelector<HTMLTextAreaElement>("#rollback-reason");
    if (!reason) throw new Error("no reason field");
    reason.value = "The MOTD change was wrong.";
    reason.dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
    submit.click();
    const id = await until(() => /^#\/plans\/(\d+)$/.exec(location.hash)?.[1]);
    const plan = await api.getPlan(Number(id));
    expect(plan.origin).toBe("rollback");
    expect(plan.reason).toBe("The MOTD change was wrong.");
    expect(plan.targets.map((t) => t.target)).toEqual(["web1"]);

    // Approve: the head equals revision 6 again.
    await api.approvePlan(plan.id);
    await api.idle();
    const head = await api.diffRevision(8, 6);
    expect(head.changes).toEqual([]);
  });

  it("explains 409 nothing-to-do", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    show(RevisionView, { api, id: 7 });
    const submit = await until(() => button("Roll back to this revision"));
    const reason = document.querySelector<HTMLTextAreaElement>("#rollback-reason");
    if (!reason) throw new Error("no reason field");
    reason.value = "Try.";
    reason.dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
    submit.click();
    const status = await until(() => document.querySelector('[role="status"]'));
    expect(status.textContent).toContain("Nothing to roll back.");
    expect(status.textContent).toContain("same documents as the head revision");
    expect(document.querySelector('[role="alert"]')).toBeNull();
    expect(location.hash).toBe("");
  });

  it("shows a 404 for an unknown revision", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    show(RevisionView, { api, id: 99 });
    const alert = await until(() => document.querySelector('[role="alert"]'));
    expect(alert.textContent).toContain("Revision 99 does not exist.");
  });
});

function required<T>(value: T | null | undefined): T {
  if (value == null) throw new Error("Missing test element");
  return value;
}

describe("History QA edges", () => {
  it("ends on an exact page boundary without duplicate revisions", async () => {
    const api = createMockApi({ signedIn: true });
    const list = vi.spyOn(api, "listRevisions");
    show(History, { api, pageSize: 1 });
    await until(() => rows().length === 1);
    for (const count of [2, 3]) {
      required(button("Load older revisions")).click();
      await until(() => rows().length === count);
    }
    expect(rows().map((r) => r?.match(/Revision \d+/)?.[0])).toEqual([
      "Revision 7",
      "Revision 6",
      "Revision 5",
    ]);
    expect(list.mock.calls.filter(([cursor]) => cursor !== undefined)).toEqual([
      ["6", 1],
      ["5", 1],
    ]);
    expect(button("Load older revisions")).toBeUndefined();
  });

  it("shows an empty history without a paging button", async () => {
    const api = createMockApi({ signedIn: true });
    vi.spyOn(api, "listRevisions").mockResolvedValue({ items: [], next: null });
    show(History, { api });
    await until(() => text().includes("No revisions yet."));
    expect(rows()).toEqual([]);
    expect(button("Load older revisions")).toBeUndefined();
  });

  it("keeps loaded rows after a paging failure and retries the same cursor", async () => {
    const api = createMockApi({ signedIn: true });
    const list = vi.spyOn(api, "listRevisions");
    show(History, { api, pageSize: 2 });
    await until(() => rows().length === 2);
    list.mockRejectedValueOnce(new Error("Paging failed"));
    required(button("Load older revisions")).click();
    await until(() => document.querySelector('[role="alert"]'));
    expect(rows()).toHaveLength(2);
    required(button("Load older revisions")).click();
    await until(() => rows().length === 3);
    expect(list.mock.calls.slice(-2)).toEqual([
      ["5", 2],
      ["5", 2],
    ]);
    expect(document.querySelector('[role="alert"]')).toBeNull();
  });

  it("renders removed documents with minus signs and keyboard focus", async () => {
    const api = createMockApi({ signedIn: true });
    const reversed = await api.diffRevision(6, 7);
    vi.spyOn(api, "diffRevision").mockResolvedValue(reversed);
    show(RevisionView, { api, id: 6 });
    await until(() => document.querySelectorAll("section.doc").length === 2);
    const removed = required(
      [...document.querySelectorAll("section.doc")].find((d) =>
        d.querySelector("h3")?.textContent?.includes("Removed"),
      ),
    );
    expect(removed.querySelector("h3")?.textContent).toContain("Block motd");
    expect(removed.querySelectorAll("tr.add")).toHaveLength(0);
    expect(removed.querySelectorAll("tr.del").length).toBeGreaterThan(0);
    expect(removed.querySelector("tr.del .text")?.textContent).toContain("−Removed: ");
    const region = required(removed.querySelector<HTMLElement>('[role="region"]'));
    region.focus();
    expect(document.activeElement).toBe(region);
  });

  it("compares the first stored revision with the empty state", async () => {
    const api = createMockApi({ signedIn: true });
    show(RevisionView, { api, id: 5 });
    await until(() => text().includes("Compared with the empty state."));
    expect(document.querySelectorAll("section.doc").length).toBeGreaterThan(0);
    expect(document.querySelectorAll("tr.del")).toHaveLength(0);
    expect(document.querySelectorAll("tr.add").length).toBeGreaterThan(0);
  });

  it("shows an empty diff without document tables", async () => {
    const api = createMockApi({ signedIn: true });
    const empty = await api.diffRevision(7, 7);
    vi.spyOn(api, "diffRevision").mockResolvedValue(empty);
    show(RevisionView, { api, id: 7 });
    await until(() => text().includes("same documents as the one before"));
    expect(document.querySelector("section.doc")).toBeNull();
    expect(text()).toContain("0 added, 0 changed, 0 removed documents");
  });

  it("rejects whitespace-only reasons even on direct form submission", async () => {
    const api = createMockApi({ signedIn: true });
    const rollback = vi.spyOn(api, "rollbackRevision");
    show(RevisionView, { api, id: 6 });
    await until(() => button("Roll back to this revision"));
    const reason = required(document.querySelector<HTMLTextAreaElement>("#rollback-reason"));
    reason.value = " \n\t ";
    reason.dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
    // The button stays enabled; an empty reason gives a field error (review #2).
    expect(button("Roll back to this revision")?.disabled).toBe(false);
    required(document.querySelector("form")).dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    );
    expect(rollback).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(reason);
    flushSync();
    expect(reason.getAttribute("aria-invalid")).toBe("true");
    expect(document.querySelector("#rollback-error")?.textContent).toBe(
      "Give a reason for the rollback.",
    );
    expect(location.hash).toBe("");
  });

  it("drops an older page that arrives after a reload of the first page", async () => {
    // Review #1: a commit while "Load older" is in flight must not leave a gap.
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const real = api.listRevisions.bind(api);
    let release: () => void = () => undefined;
    vi.spyOn(api, "listRevisions").mockImplementation(async (cursor, limit) => {
      if (cursor) await new Promise<void>((r) => (release = r));
      return real(cursor, limit);
    });
    show(History, { api, pageSize: 2 });
    await until(() => rows().length === 2);
    button("Load older revisions")?.click();
    await changeWeb1(api, 4, "New head.");
    await until(() => rows()[0]?.includes("New head."), 3000);
    release();
    await new Promise((r) => setTimeout(r, 20));
    flushSync();
    expect(rows().map((r) => /Revision (\d+)/.exec(r ?? "")?.[1])).toEqual(["8", "7"]);
    expect(button("Load older revisions")?.disabled).toBe(false);
  });
});

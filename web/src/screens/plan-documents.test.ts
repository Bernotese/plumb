// @vitest-environment happy-dom
// T-168 (host finding F1 of T-158): the Plan screen shows the document
// changes that the approval commits (Plan.documents, T-170), also for a
// Plan without Targets.
import { afterEach, describe, expect, it } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { DocumentChange, Plan } from "../lib/types";
import PlanView from "./PlanView.svelte";
import { button, reset, show, text, until } from "./testkit";

afterEach(reset);

const block = (name: string, message: string) => ({
  kind: "Block" as const,
  name,
  spec: {
    title: name,
    params: {
      type: "object",
      properties: { message: { type: "string", title: "Message", default: message } },
    },
    resources: [{ file: { path: `/etc/${name}`, content: "{{ .message }}\n" } }],
  },
});

const diffOf = (i: number) => document.querySelector(`section[aria-labelledby="pdoc-${i}-title"]`);

/** Adds a Block that no Target uses, and approves it. */
async function addBlock(api: ReturnType<typeof createMockApi>, name: string, message: string) {
  const p = await api.createPlan({ reason: `Add ${name}.`, put: [block(name, message) as never] });
  await api.approvePlan(p.id);
}

/** The mock with getPlan answering the given Plan fields on top of plan 14. */
async function withPlan(fields: Partial<Plan>) {
  const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
  const base = await api.getPlan(14);
  api.getPlan = async () => ({ ...base, ...fields });
  return api;
}

describe("document changes of a Plan", () => {
  it("an update of a Block without Targets shows its diff before approval", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    await addBlock(api, "lonely", "Old text.");
    const plan = await api.createPlan({
      reason: "Update the Block lonely.",
      put: [block("lonely", "New text.") as never],
    });
    expect(plan.targets).toEqual([]);
    expect(plan.documents?.map((c) => [c.kind, c.name, c.op])).toEqual([
      ["Block", "lonely", "changed"],
    ]);

    show(PlanView, { api, id: plan.id });
    await until(() => diffOf(0)?.querySelector("table.diff"));
    expect(document.querySelector("#docs-count")?.textContent?.replace(/\s+/g, " ")).toContain(
      "Approving commits 0 added, 1 changed, 0 removed document",
    );
    expect(text()).toContain("Secret values are never shown");
    expect(document.querySelector("#pdoc-0-title")?.textContent).toMatch(/Changed\s*Block lonely/);
    const changed = diffOf(0)?.textContent ?? "";
    expect(changed).toMatch(/Removed:\s*default: Old text\./);
    expect(changed).toMatch(/Added:\s*default: New text\./);
    expect(button("Approve and apply")).toBeDefined();
  });

  it("a catalogue update lists the Block with the stored one as before", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const motd = (await api.listCatalogBlocks()).find((c) => c.name === "motd");
    const stored = (await api.listBlocks()).find((b) => b.document.name === "motd")?.document;
    const plan = await api.createPlan({
      reason: "Update the Block motd from the catalogue.",
      put: [motd?.document as never],
    });
    expect(plan.documents).toEqual([
      { kind: "Block", name: "motd", op: "changed", before: stored, after: motd?.document },
    ]);
  });

  it("the mock keeps a copy of the proposal (a screen passes a state proxy)", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const doc = new Proxy(block("proxied", "x"), {});
    const plan = await api.createPlan({ reason: "Proxy.", put: [doc as never] });
    expect(plan.documents?.[0]?.name).toBe("proxied");
    expect((await api.getPlan(plan.id)).documents?.[0]?.op).toBe("added");
  });

  it("an added and a removed document, in kind and name order", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    await addBlock(api, "aa-old", "x");
    const plan = await api.createPlan({
      reason: "Two blocks.",
      put: [block("zz-new", "hi") as never],
      delete: [{ kind: "Block", name: "aa-old" }],
    });
    show(PlanView, { api, id: plan.id });
    await until(() => diffOf(1));
    expect(document.querySelector("#pdoc-0-title")?.textContent).toMatch(/Removed\s*Block aa-old/);
    expect(document.querySelector("#pdoc-1-title")?.textContent).toMatch(/Added\s*Block zz-new/);
    expect(document.querySelector("#docs-count")?.textContent).toMatch(
      /1 added, 0 changed, 1 removed documents/,
    );
  });

  it("an approved Plan shows the diff of its committed revision", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    await addBlock(api, "lonely", "Old text.");
    const plan = await api.createPlan({
      reason: "Update the Block lonely.",
      put: [block("lonely", "New text.") as never],
    });
    await api.approvePlan(plan.id);
    const done = await api.getPlan(plan.id);
    expect(done.status).toBe("approved");
    expect(done.documents).toBeUndefined();
    show(PlanView, { api, id: plan.id });
    await until(() => diffOf(0)?.querySelector("table.diff"));
    expect(document.querySelector("#docs-revision")?.textContent).toContain(
      `The approval committed revision ${done.revision}.`,
    );
    expect(document.querySelector("#docs-count")?.textContent?.replace(/\s+/g, " ")).toContain(
      "The approval committed 0 added, 1 changed, 0 removed document",
    );
    expect(diffOf(0)?.textContent).toMatch(/Added:\s*default: New text\./);
  });

  it("a Plan whose list the server cannot compute says so", async () => {
    const api = await withPlan({ documents: undefined, documents_unavailable: true });
    show(PlanView, { api, id: 14 });
    await until(() => document.querySelector("#docs-unavailable"));
    expect(text()).toContain("The document changes of this plan are not available");
    expect(document.querySelector("#docs-missing, #docs-count, #docs-none")).toBeNull();
  });

  it("an approved Plan without a revision changed no document", async () => {
    const api = await withPlan({
      origin: "agent_update",
      status: "approved",
      revision: null,
      documents: undefined,
    });
    show(PlanView, { api, id: 14 });
    await until(() => document.querySelector("#docs-none"));
    expect(text()).toContain("This plan changed no document.");
  });

  it("an agent update changes no document", async () => {
    const api = await withPlan({ origin: "agent_update", targets: [], documents: [] });
    show(PlanView, { api, id: 14 });
    await until(() => document.querySelector("#docs-none"));
    expect(text()).toContain("This plan changes no document.");
  });

  it("an older server without the field says so", async () => {
    const api = await withPlan({ documents: undefined });
    show(PlanView, { api, id: 14 });
    await until(() => document.querySelector("#docs-missing"));
    expect(text()).toContain("This server does not list the document changes of a plan.");
  });

  it("shows the first 20 diffs, the rest on request, and names truncated entries", async () => {
    const docs: DocumentChange[] = Array.from({ length: 23 }, (_, i) => ({
      kind: "Block",
      name: `b${String(i).padStart(2, "0")}`,
      op: "added",
      before: null,
      after: i < 21 ? (block(`b${i}`, "x") as never) : null,
    }));
    const api = await withPlan({ documents: docs, documents_truncated: true });
    show(PlanView, { api, id: 14 });
    await until(() => document.querySelector("#docs-all"));
    expect(document.querySelectorAll("section.doc").length).toBe(20);
    expect(text()).toContain("Some documents are listed by name only.");
    button("Show the other 3 documents")?.click();
    await until(() => document.querySelector("#pdoc-22"));
    expect(document.querySelectorAll("section.doc").length).toBe(21);
    expect(document.querySelector("#pdoc-21")?.textContent).toMatch(
      /Added\s*Block b21\s*\(too large to show here\)/,
    );
    expect(document.querySelector("#docs-all")).toBeNull();
  });
});

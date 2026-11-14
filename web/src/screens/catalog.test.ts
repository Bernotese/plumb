// @vitest-environment happy-dom
// T-154: the starter catalogue on the Blocks page (D-328) on the mock.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import { me } from "../lib/session.svelte";
import Blocks from "./Blocks.svelte";
import { button, planOpened, reset, show, text, until } from "./testkit";

afterEach(() => {
  reset();
  me.session = null;
});

const catalogRows = () =>
  [...([...document.querySelectorAll("table")][1]?.querySelectorAll("tbody tr") ?? [])].map(
    (t) => t.textContent?.replace(/\s+/g, " ").trim() ?? "",
  );

describe("starter catalogue", () => {
  it("lists the catalogue with its state against the stored Blocks", async () => {
    const api = createMockApi({ signedIn: true });
    show(Blocks, { api });
    await until(() => catalogRows().length === 4);
    const r = catalogRows();
    expect(r.find((x) => x.startsWith("motd"))).toContain("added, differs");
    expect(r.find((x) => x.startsWith("nginx"))).toContain("added, same");
    expect(r.find((x) => x.startsWith("unattended-upgrades"))).toContain("not added");
    expect(r.find((x) => x.startsWith("sysctl-hardening"))).toMatch(/VM\s*not added/);
    expect(button("Add nginx")).toBeUndefined();
  });

  it("Add makes a normal proposal with the catalogue document", async () => {
    const api = createMockApi({ signedIn: true });
    const create = vi.spyOn(api, "createPlan");
    show(Blocks, { api });
    (await until(() => button("Add unattended-upgrades"))).click();
    const id = await planOpened();
    const doc = (await api.listCatalogBlocks()).find((c) => c.name === "unattended-upgrades");
    expect(create).toHaveBeenCalledWith({
      reason: "Add the starter Block unattended-upgrades from the catalogue.",
      put: [doc?.document],
    });
    expect((await api.getPlan(id)).status).toBe("pending");
    // Nothing is stored before the approval.
    expect((await api.listBlocks()).some((b) => b.document.name === "unattended-upgrades")).toBe(
      false,
    );
  });

  it("a differing Block shows the difference and offers an update plan", async () => {
    const api = createMockApi({ signedIn: true });
    const create = vi.spyOn(api, "createPlan");
    show(Blocks, { api });
    (await until(() => button("Show difference"))).click();
    await until(() => document.querySelector("#catalog-diff-motd section.doc"));
    expect(text()).toContain("Yours (−) and the catalogue (+).");
    button("Update from the catalogue: motd")?.click();
    await planOpened();
    expect(create.mock.calls[0]?.[0].reason).toBe("Update the Block motd from the catalogue.");
  });

  it("an older server without the catalogue still lists the Blocks", async () => {
    const api = createMockApi({ signedIn: true });
    vi.spyOn(api, "listCatalogBlocks").mockRejectedValue(new Error("not found"));
    show(Blocks, { api });
    await until(() => text().includes("not found"));
    expect(document.querySelectorAll("table").length).toBe(1);
  });

  it("the role read sees the catalogue without Add", async () => {
    const api = createMockApi({ role: "read" });
    me.session = await api.session();
    show(Blocks, { api });
    await until(() => catalogRows().length === 4);
    expect(button("Add unattended-upgrades")).toBeUndefined();
    expect(button("New block")).toBeUndefined();
    expect(document.querySelector('a[href="#/blocks/new"]')).toBeNull();
    expect(text()).toContain("Your role is read. This needs the role plan or higher.");
  });
});

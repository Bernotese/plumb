// @vitest-environment happy-dom
// Screen tests of T-071 on the mock: addresses reported by the guest, the
// mismatch line, the Issue on guest:address (only ignore, D-215).
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import { ADDRESS_HINT, REPORTED_MARK, REPORTED_TITLE } from "../lib/view";
import IssueView from "./IssueView.svelte";
import TargetForm from "./TargetForm.svelte";
import Targets from "./Targets.svelte";
import { button, el, reset, rows, show, text, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

describe("target list", () => {
  it("shows the configured address and the reported list with its mark", async () => {
    show(Targets, { api: adminApi() });
    const r = await until(() => rows().length > 1 && rows());
    const db1 = r.find((x) => x.startsWith("db1"));
    expect(db1).toContain("192.168.1.20");
    expect(db1).toContain(`192.168.1.107 ${REPORTED_MARK}`);
    // A DHCP Target shows its address for the first time.
    const web1 = r.find((x) => x.startsWith("web1"));
    expect(web1).toContain(`Configured: DHCP192.168.1.41, fd00::41 ${REPORTED_MARK}`);
  });

  it("shows no reported list when Plumb has none", async () => {
    const api = adminApi();
    const list = await api.listTargets();
    const off = list.find((t) => t.status.reported_at === null);
    expect(off).toBeDefined();
    show(Targets, { api });
    const r = await until(() => rows().length > 1 && rows());
    expect(r.find((x) => x.startsWith(off?.document.name ?? "?"))).not.toContain(REPORTED_MARK);
  });

  it("marks a list with more than 16 addresses", async () => {
    const api = adminApi();
    const list = await api.listTargets();
    vi.spyOn(api, "listTargets").mockResolvedValue(
      list.map((t) =>
        t.document.name === "db1" ? { ...t, status: { ...t.status, reported_ips_more: true } } : t,
      ),
    );
    show(Targets, { api });
    const r = await until(() => rows().length > 1 && rows());
    expect(r.find((x) => x.startsWith("db1"))).toContain(`192.168.1.107 + more ${REPORTED_MARK}`);
  });

  it("escapes a list that the guest chose", async () => {
    const api = adminApi();
    const list = await api.listTargets();
    vi.spyOn(api, "listTargets").mockResolvedValue(
      list.map((t) =>
        t.document.name === "db1"
          ? { ...t, status: { ...t.status, reported_ips: ["<img src=x onerror=alert(1)>"] } }
          : t,
      ),
    );
    show(Targets, { api });
    await until(() => rows().length > 1);
    expect(document.querySelector("img")).toBeNull();
    expect(text()).toContain("<img src=x onerror=alert(1)>");
  });
});

describe("target page", () => {
  it("shows the mismatch line with a link to the Issue", async () => {
    show(TargetForm, { api: adminApi(), name: "db1" });
    const line = await until(() => document.querySelector("#addr-mismatch"));
    expect(text(line)).toContain("Configured: 192.168.1.20 — the guest reports 192.168.1.107");
    expect(el<HTMLAnchorElement>("#addr-mismatch a").getAttribute("href")).toBe("#/issues/10");
    expect(text()).toContain("Reported by the guest");
  });

  it("shows no mismatch line on a Target without an address Issue", async () => {
    show(TargetForm, { api: adminApi(), name: "web1" });
    await until(() => text().includes("192.168.1.41, fd00::41"));
    expect(document.querySelector("#addr-mismatch")).toBeNull();
    expect(text()).toContain("DHCP");
  });
});

describe("Issue on guest:address", () => {
  it("offers only the two ignore actions and the hint", async () => {
    show(IssueView, { api: adminApi(), id: 10 });
    await until(() => document.querySelector("#address-text"));
    expect(text(el("#address-text"))).toBe(
      "The guest does not use the configured address 192.168.1.20. It reports: 192.168.1.107.",
    );
    expect(text()).toContain("in the address list that the agent in the guest reports");
    expect(text(el("#address-hint"))).toBe(ADDRESS_HINT);
    expect(button(/^Revert/)).toBeUndefined();
    expect(button(/^Accept/)).toBeUndefined();
    expect(text()).not.toContain("This makes a plan.");
    expect(button("Ignore once")).toBeDefined();
    expect(button("Make ignore-rule plan")).toBeDefined();
    expect(text()).toContain("Reported by the guest");
  });

  it("ignores once", async () => {
    const api = adminApi();
    show(IssueView, { api, id: 10 });
    (await until(() => button("Ignore once"))).click();
    (await until(() => button("Ignore this drift"))).click();
    await until(() => text().includes("Ignored once."));
    expect((await api.getIssue(10)).status).toBe("resolved");
  });

  it("the mock refuses revert with 409", async () => {
    await expect(adminApi().resolveIssue(10, "revert")).rejects.toMatchObject({
      problem: { status: 409 },
    });
  });
});

describe("review T-071", () => {
  it("S1: the Target page asks only for the open Issues of its Target", async () => {
    const api = adminApi();
    const spy = vi.spyOn(api, "listIssues");
    show(TargetForm, { api, name: "db1" });
    await until(() => document.querySelector("#addr-mismatch"));
    expect(spy).toHaveBeenCalledWith({ target: "db1", status: "open" });
  });

  it("N1: the help text is visible and describes the reported list", async () => {
    show(TargetForm, { api: adminApi(), name: "db1" });
    const help = await until(() => document.querySelector("#addr-help"));
    expect(text(help)).toBe(REPORTED_TITLE);
    expect(document.querySelector('[aria-describedby="addr-help"]')).not.toBeNull();
  });
});

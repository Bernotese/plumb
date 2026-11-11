// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import { REPORTED_MARK } from "../lib/view";
import IssueView from "./IssueView.svelte";
import TargetForm from "./TargetForm.svelte";
import Targets from "./Targets.svelte";
import { el, reset, rows, show, text, until } from "./testkit";

afterEach(reset);
const payload = '<img src=x onerror="alert(1)">';
const apiFor = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

describe("address display QA", () => {
  it.each(["list", "detail"])("hides stale data with null reported_at on %s", async (screen) => {
    const api = apiFor();
    const target = await api.getTarget("db1");
    const stale = {
      ...target,
      status: {
        ...target.status,
        reported_at: null,
        reported_ips: ["203.0.113.99"],
        reported_ips_more: true,
      },
    };
    if (screen === "list") {
      vi.spyOn(api, "listTargets").mockResolvedValue([stale]);
      show(Targets, { api });
      await until(() => rows().length === 1);
      expect(text()).not.toContain(REPORTED_MARK);
    } else {
      vi.spyOn(api, "getTarget").mockResolvedValue(stale);
      show(TargetForm, { api, name: "db1" });
      await until(() => text().includes("No list: the agent is offline"));
    }
    expect(text()).not.toContain("203.0.113.99");
    expect(text()).not.toContain("+ more");
  });

  it("shows configured and reported addresses with more on the Target page", async () => {
    const api = apiFor();
    const target = await api.getTarget("db1");
    vi.spyOn(api, "getTarget").mockResolvedValue({
      ...target,
      status: { ...target.status, reported_ips_more: true },
    });
    show(TargetForm, { api, name: "db1" });
    const card = await until(() => document.querySelector(".addr-card"));
    expect(text(card)).toContain("Configured 192.168.1.20");
    expect(text(card)).toContain("Reported by the guest 192.168.1.107 + more");
  });

  it.each(["target", "issue"])("renders guest values as text on the %s page", async (screen) => {
    const api = apiFor();
    const issue = await api.getIssue(10);
    const injected = {
      ...issue,
      changes: [{ attr: "ip", from: [payload], to: payload, risk: "read" as const }],
    };
    if (screen === "target") {
      const target = await api.getTarget("db1");
      vi.spyOn(api, "getTarget").mockResolvedValue({
        ...target,
        status: { ...target.status, ips: [payload], reported_ips: [payload] },
      });
      vi.spyOn(api, "listIssues").mockResolvedValue([injected]);
      show(TargetForm, { api, name: "db1" });
      await until(() => document.querySelector("#addr-mismatch"));
      expect(text(el("#addr-mismatch"))).toContain(payload);
      expect(text(el(".addr-card"))).toContain(`Reported by the guest ${payload}`);
    } else {
      vi.spyOn(api, "getIssue").mockResolvedValue(injected);
      show(IssueView, { api, id: 10 });
      await until(() => document.querySelector("#address-text"));
      expect(text(el("#address-text"))).toContain(payload);
    }
    expect(document.querySelector("img")).toBeNull();
    expect(document.querySelector("[onerror]")).toBeNull();
  });
});

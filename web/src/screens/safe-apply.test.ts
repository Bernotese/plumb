// @vitest-environment happy-dom
// T-118: the T-111 display parts: firewall summary and confirm window in the
// Plan, a rolled-back step in the Run, notes on grow and stop (D-291, D-300).
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { Plan, Run } from "../lib/types";
import { changeNotes } from "../lib/view";
import PlanView from "./PlanView.svelte";
import RunView from "./RunView.svelte";
import { reset, show, text, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

describe("safe apply in Plan and Run", () => {
  it("shows the summary lines and the confirm window of a firewall change", async () => {
    const api = adminApi();
    const base = await api.getPlan(api.orderPlanId ?? 0);
    const plan: Plan = {
      ...base,
      targets: [
        {
          target: "web1",
          resources: [
            {
              resource: "firewall:main",
              action: "update",
              risk: "disruptive",
              changes: [{ attr: "policy", from: "accept", to: "drop", risk: "disruptive" }],
              summary: ["SSH tcp/22 open from everywhere", "policy drop: other input is dropped"],
              confirm_seconds: 90,
            },
          ],
        },
      ],
    } as Plan;
    vi.spyOn(api, "getPlan").mockResolvedValue(plan);
    show(PlanView, { api, id: plan.id });
    await until(() => document.querySelector("section.resource"));
    const lines = [...document.querySelectorAll(".summary li")].map((l) => l.textContent);
    expect(lines).toEqual([
      "SSH tcp/22 open from everywhere",
      "policy drop: other input is dropped",
    ]);
    expect(text()).toContain("must reach Plumb again within 90 s");
  });

  it("marks a rolled-back step in the Run", async () => {
    const api = adminApi();
    const base = await api.getRun(18).catch(() => null);
    const run = {
      ...(base ?? {}),
      id: 18,
      status: "failed",
      targets: [
        {
          target: "web1",
          status: "failed",
          steps: [
            {
              resource: "firewall:main",
              action: "update",
              status: "failed",
              changes: [],
              output: "no confirm within 90 s",
              rolled_back: true,
              started_at: null,
              finished_at: null,
            },
          ],
        },
      ],
    } as unknown as Run;
    vi.spyOn(api, "getRun").mockResolvedValue(run);
    show(RunView, { api, id: 18 });
    await until(() => document.querySelector(".rolled"));
    expect(text()).toContain("put the old state back");
  });
});

describe("day 2 change notes", () => {
  it("says a grow cannot be undone and what a stop does", () => {
    const grow = changeNotes(
      { attr: "disk", from: 20, to: 40, risk: "disruptive" },
      "guest:config",
    );
    expect(grow.notes.join(" ")).toContain("A grow cannot be undone");
    const stop = changeNotes(
      { attr: "power", from: "running", to: "stopped", risk: "disruptive" },
      "guest:config",
    );
    expect(stop.notes.join(" ")).toContain("Agent steps are skipped");
  });
});

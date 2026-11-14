// @vitest-environment happy-dom
// T-205 (D-396, condition 2 of pl-lead): a Run with `warning` steps never
// reads as a clean success – in the Run list, the Run header, the Target
// of the Run and the step.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { Run } from "../lib/types";
import RunView from "./RunView.svelte";
import Runs from "./Runs.svelte";
import { reset, show, text, until } from "./testkit";

afterEach(reset);

const run: Run = {
  id: 77,
  plan_id: 5,
  revision: 12,
  status: "succeeded",
  started_at: "2026-11-14T09:00:00Z",
  finished_at: "2026-11-14T09:02:00Z",
  waits_for_restart: 0,
  warnings: 1,
  targets: [
    {
      target: "wiki",
      status: "succeeded",
      steps: [
        {
          resource: "guest:config",
          action: "delete",
          status: "warning",
          changes: [],
          output:
            "The guest 150 still has the Plumb agent and the repair key line: the agent is offline.",
          started_at: "2026-11-14T09:00:00Z",
          finished_at: "2026-11-14T09:02:00Z",
        },
      ],
    },
  ],
};

describe("Run warnings", () => {
  it("marks the Run, the Target and the step in the Run view", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    vi.spyOn(api, "getRun").mockResolvedValue(run);
    show(RunView, { api, id: 77 });
    await until(() => document.querySelector("tbody tr"));
    const page = text(document.body);
    expect(page).toContain("succeeded, 1 warning");
    expect(page).toContain("succeeded, with warnings");
    const cells = [...(document.querySelector("tbody tr")?.children ?? [])].map((c) =>
      c.textContent?.trim(),
    );
    expect(cells[2]).toBe("warning");
  });

  it("marks the Run in the Run list", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const page0 = await api.listRuns();
    vi.spyOn(api, "listRuns").mockResolvedValue({ ...page0, items: [run] });
    show(Runs, { api });
    await until(() => text(document.body).includes("succeeded, 1 warning"));
  });
});

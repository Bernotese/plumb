// @vitest-environment happy-dom
// Screen tests of T-049: apply order (`after`) and the cause of a restart
// in the Plan view, the output of a skipped step in the Run view.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { Run } from "../lib/types";
import PlanView from "./PlanView.svelte";
import RunView from "./RunView.svelte";
import { reset, show, text, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

/** The text of the resource card of `id`. */
const card = (id: string) =>
  text(
    [...document.querySelectorAll("section.resource")].find((s) =>
      s.querySelector("h3")?.textContent?.includes(id),
    ) ?? document.createElement("div"),
  );

describe("PlanView order", () => {
  it("shows after on the waiting resource and the cause of a restart", async () => {
    const api = adminApi();
    const demo = await api.getPlan(api.orderPlanId ?? 0);
    show(PlanView, { api, id: demo.id });
    await until(() => document.querySelector("section.resource"));

    expect(card("file:/etc/nginx/sites-available/a")).toContain("after package:nginx");
    expect(card("file:/etc/nginx/sites-available/b")).toContain("after package:nginx");
    expect(card("symlink:/etc/nginx/sites-enabled/b")).not.toContain("after");
    const restart = [...document.querySelectorAll("section.resource.restart")];
    expect(restart).toHaveLength(1);
    const t = text(restart[0]);
    expect(t).toContain("restart because of file:/etc/nginx/sites-available/a, ");
    expect(t).toContain("file:/etc/nginx/sites-available/b, symlink:/etc/nginx/sites-enabled/b");
    expect(t).not.toContain("a resource it depends on changes");
    // No new sort: the Plan keeps the list order of the mock.
    const order = [...document.querySelectorAll("section.resource h3 .mono")].map(
      (e) => e.textContent,
    );
    expect(order).toEqual(demo.targets[0]?.resources.map((r) => r.resource));
  });

  it("keeps the general text of a restart without restart_by", async () => {
    const api = adminApi();
    const plan = await api.getPlan(api.orderPlanId ?? 0);
    for (const r of plan.targets[0]?.resources ?? []) delete r.restart_by;
    vi.spyOn(api, "getPlan").mockResolvedValue(plan);
    show(PlanView, { api, id: plan.id });
    await until(() => document.querySelector("section.resource.restart"));
    const t = text(document.querySelector("section.resource.restart") ?? undefined);
    expect(t).toContain("The service restarts because a resource it depends on changes.");
    expect(t).not.toContain("restart because of");
  });
});

describe("RunView skipped step", () => {
  it("shows the output that names the cause", async () => {
    const api = adminApi();
    const run: Run = {
      id: 99,
      plan_id: 1,
      revision: 9,
      status: "failed",
      started_at: "2026-11-10T09:00:00Z",
      finished_at: "2026-11-10T09:01:00Z",
      targets: [
        {
          target: "web1",
          status: "failed",
          steps: [
            {
              resource: "package:nginx",
              action: "create",
              status: "failed",
              changes: [],
              output: "E: Unable to locate package nginx",
              started_at: "2026-11-10T09:00:00Z",
              finished_at: "2026-11-10T09:00:30Z",
            },
            {
              resource: "file:/etc/nginx/sites-available/a",
              action: "create",
              status: "skipped",
              changes: [],
              output: "not applied: package:nginx was failed",
              started_at: null,
              finished_at: null,
            },
          ],
        },
      ],
    };
    vi.spyOn(api, "getRun").mockResolvedValue(run);
    show(RunView, { api, id: 99 });
    await until(() => document.querySelector("tbody tr"));
    const cells = [...document.querySelectorAll("tbody tr")].map((r) =>
      [...r.children].map((c) => c.textContent?.trim()),
    );
    expect(cells[1]).toEqual([
      "file:/etc/nginx/sites-available/a",
      "create",
      "skipped",
      "not applied: package:nginx was failed",
    ]);
  });
});

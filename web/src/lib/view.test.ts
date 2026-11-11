import { describe, expect, it } from "vitest";
import type { Issue, Plan, TargetView } from "./types";
import {
  addressIssueText,
  applyStepEvent,
  changeView,
  humanOnlyReasons,
  maxRisk,
  mismatchLine,
  reportedAddrs,
  reportedText,
  targetState,
  valueText,
} from "./view";

function target(
  phase: TargetView["status"]["phase"],
  agent: TargetView["status"]["agent"] = "online",
  open = 0,
): TargetView {
  return {
    revision: 1,
    document: {
      kind: "Target",
      name: "t",
      spec: { type: "vm", image: "i", guest: { cores: 1, memory: 256, disk: 1, bridge: "vmbr0" } },
    },
    status: { phase, agent, power: "running", open_issues: open, waiting: [] },
  };
}

describe("targetState", () => {
  it.each([
    [target("ready"), "In Plumb", "ok"],
    [target("drifted", "online", 2), "Out of Plumb", "warn"],
    [target("applying"), "Applying", "info"],
    [target("provisioning"), "Applying", "info"],
    [target("ready", "offline"), "Offline", "muted"],
    [target("drifted", "offline", 1), "Offline", "muted"],
    [target("error"), "Error", "danger"],
    [target("pending"), "Pending", "muted"],
  ])("%#: %s", (t, label, tone) => {
    expect(targetState(t)).toMatchObject({ label, tone });
  });
  it("counts the open issues", () => {
    expect(targetState(target("drifted", "online", 1)).detail).toBe("1 open issue.");
  });
});

describe("changes", () => {
  it("never shows a sensitive value", () => {
    expect(changeView({ attr: "content", sensitive: true, risk: "reversible" })).toEqual({
      attr: "content",
      from: "",
      to: "",
      hidden: true,
      restart: false,
    });
  });
  it("shows a restart without values", () => {
    expect(changeView({ attr: "restart", risk: "disruptive" })).toMatchObject({
      restart: true,
      hidden: false,
    });
  });
  it("shows missing values as not there", () => {
    expect(changeView({ attr: "content", to: "x", risk: "reversible" })).toMatchObject({
      from: "(not there)",
      to: "x",
    });
  });
  it.each([
    [undefined, "(not there)"],
    ["", '""'],
    [["a", "b"], "a\nb"],
    [[], "(empty list)"],
    [4, "4"],
    [true, "true"],
  ])("valueText(%o) = %s", (v, want) => {
    expect(valueText(v)).toBe(want);
  });
  it("finds the highest risk", () => {
    expect(maxRisk([])).toBe("read");
    expect(maxRisk(["reversible", "destructive", "disruptive"])).toBe("destructive");
  });
});

describe("applyStepEvent", () => {
  const run = () => ({
    id: 3,
    targets: [{ target: "web1", steps: [{ resource: "file:/a", status: "pending" }] }],
  });
  it("updates the matching step only", () => {
    const r = run();
    expect(
      applyStepEvent(r, {
        type: "run.step",
        subject: "run:3",
        data: { target: "web1", resource: "file:/a", status: "applied" },
      }),
    ).toBe(true);
    expect(r.targets[0]?.steps[0]?.status).toBe("applied");
    expect(
      applyStepEvent(r, {
        type: "run.step",
        subject: "run:4",
        data: { target: "web1", resource: "file:/a", status: "failed" },
      }),
    ).toBe(false);
    expect(applyStepEvent(r, { type: "run.finished", subject: "run:3", data: {} })).toBe(false);
    expect(
      applyStepEvent(r, {
        type: "run.step",
        subject: "run:3",
        data: { target: "x", resource: "file:/a", status: "failed" },
      }),
    ).toBe(false);
    expect(r.targets[0]?.steps[0]?.status).toBe("applied");
  });
});

describe("humanOnlyReasons (D-099)", () => {
  const base: Omit<Plan, "targets"> = {
    id: 1,
    origin: "proposal",
    issue_id: null,
    status: "pending",
    risk: "reversible",
    reason: "r",
    author: "user:admin",
    created_at: "2026-11-10T09:00:00Z",
    base_revision: 7,
    approved_by: null,
    approved_at: null,
    revision: null,
    run_id: null,
    warnings: [],
  };
  it("is empty for a normal plan", () => {
    expect(humanOnlyReasons({ ...base, targets: [] })).toEqual([]);
  });
  it("names the secrets and the target", () => {
    const plan = {
      ...base,
      human_only: ["secret-binding", "rule-change"],
      targets: [
        {
          target: "web1",
          secrets: [{ name: "app-db", version: 4 }],
          resources: [
            {
              resource: "file:/etc/app/db.conf",
              action: "create",
              risk: "reversible",
              changes: [{ attr: "content", sensitive: true, risk: "reversible" }],
            },
          ],
        },
        { target: "db1", secrets: [{ name: "x", version: 1 }], resources: [] },
      ],
    } satisfies Plan;
    expect(humanOnlyReasons(plan)).toEqual([
      "It gives a secret to web1 that web1 did not get before. web1 uses: app-db.",
      "It adds, changes or removes an ignore rule.",
    ]);
  });
  it("has a general text when no target lists the secret", () => {
    expect(humanOnlyReasons({ ...base, human_only: ["secret-binding"], targets: [] })).toEqual([
      "It gives a secret to a target that did not get it before.",
    ]);
  });
});

describe("reported addresses (T-071)", () => {
  const st = (s: Partial<TargetView["status"]>) =>
    ({ ...target("ready").status, ...s }) as TargetView["status"];
  const issue = (from: unknown, to: unknown) =>
    ({ changes: [{ attr: "ip", from, to, risk: "read" }] }) as unknown as Issue;

  it("has no list while reported_at is null", () => {
    expect(reportedAddrs(st({ reported_ips: [], reported_at: null }))).toBeNull();
    expect(reportedAddrs(st({}))).toBeNull();
  });

  it.each([
    [[], false, "no address"],
    [["192.0.2.1", "fd00::1"], false, "192.0.2.1, fd00::1"],
    [["192.0.2.1"], true, "192.0.2.1 + more"],
  ])("%#: %j more=%s", (ips, more, out) => {
    const r = reportedAddrs(
      st({ reported_ips: ips, reported_at: "2026-11-10T09:00:00Z", reported_ips_more: more }),
    );
    expect(r && reportedText(r)).toBe(out);
  });

  it("writes the Issue text and the mismatch line", () => {
    const i = issue(["192.0.2.107"], "192.0.2.41");
    expect(addressIssueText(i)).toBe(
      "The guest does not use the configured address 192.0.2.41. It reports: 192.0.2.107.",
    );
    expect(mismatchLine(i)).toBe("Configured: 192.0.2.41 — the guest reports 192.0.2.107");
    expect(addressIssueText(issue([], "192.0.2.41"))).toContain("It reports: no address.");
  });
});

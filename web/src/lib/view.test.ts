import { describe, expect, it } from "vitest";
import type { TargetView } from "./types";
import { changeView, maxRisk, targetState, valueText } from "./view";

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
    status: { phase, agent, power: "running", open_issues: open },
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

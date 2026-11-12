import { describe, expect, it } from "vitest";
import { agentState, missingText, outdatedOf } from "./agent";
import { ApiError } from "./api";
import type { TargetView } from "./types";

const status = (s: Partial<TargetView["status"]>) => s as TargetView["status"];

describe("agent helpers", () => {
  it("maps every update state", () => {
    expect(agentState(status({}))).toBeUndefined();
    expect(agentState(status({ agent_update: "none" }))).toMatchObject({
      label: "",
      canUpdate: false,
    });
    expect(agentState(status({ agent_update: "available" }))).toMatchObject({
      label: "Update available",
      canUpdate: true,
    });
    expect(agentState(status({ agent_update: "required" }))).toMatchObject({
      tone: "danger",
      canUpdate: true,
    });
    expect(agentState(status({ agent_update: "manual" }))?.text).toContain("plumb-agent-install");
    expect(agentState(status({ agent_update: "unknown" }))?.canUpdate).toBe(false);
  });

  it("names a missing type or attribute", () => {
    expect(missingText("unit")).toBe("resource type unit");
    expect(missingText("file.acl")).toBe("attribute acl of file");
    expect(missingText("a.b.c")).toBe("attribute b.c of a");
  });

  it("reads the agent-outdated refusal only", () => {
    const err = new ApiError({
      type: "urn:plumb:problem:agent-outdated",
      title: "Agent too old",
      status: 409,
      detail: "update the agent first",
      targets: ["web1"],
      missing: ["unit"],
    });
    expect(outdatedOf(err)).toEqual({
      detail: "update the agent first",
      targets: ["web1"],
      missing: ["unit"],
    });
    expect(
      outdatedOf(new ApiError({ type: "urn:plumb:problem:plan-stale", title: "x", status: 409 })),
    ).toBeUndefined();
    expect(outdatedOf(new Error("x"))).toBeUndefined();
  });
});

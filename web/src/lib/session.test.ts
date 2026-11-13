import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import roles from "./roles.gen.json";
import { can, canApprove, canTakeRisk, roleOf } from "./session.svelte";
import type { Plan, Session } from "./types";

const person = (tier: Session["tier"]): Session => ({
  principal: "user:x",
  must_change_password: false,
  tier,
  expires_at: null,
});

describe("roles.gen.json (D-342, read from api/openapi.yaml)", () => {
  it("has every operation of the contract", () => {
    const yaml = readFileSync(new URL("../../../api/openapi.yaml", import.meta.url), "utf8");
    const ids = [...yaml.matchAll(/^\s+operationId: (\w+)$/gmu)].map((m) => m[1]);
    expect(ids.length).toBeGreaterThan(40);
    expect(Object.keys(roles).sort()).toEqual([...new Set(ids)].sort());
  });

  it("follows the rule: x-plumb-role, else the lowest token tier, else admin", () => {
    expect(roles.login).toBe("none");
    expect(roles.getSession).toBe("read"); // x-plumb-role
    expect(roles.changePassword).toBe("read"); // x-plumb-role, session only
    expect(roles.listTargets).toBe("read"); // the default security
    expect(roles.createPlan).toBe("plan");
    expect(roles.approvePlan).toBe("apply");
    expect(roles.listUsers).toBe("admin"); // session only
    expect(roles.createToken).toBe("admin");
  });
});

describe("can", () => {
  it("compares the role with the operation", () => {
    expect(can("listTargets", person("read"))).toBe(true);
    expect(can("createPlan", person("read"))).toBe(false);
    expect(can("createPlan", person("plan"))).toBe(true);
    expect(can("approvePlan", person("plan"))).toBe(false);
    expect(can("approvePlan", person("apply"))).toBe(true);
    expect(can("createUser", person("apply"))).toBe(false);
    expect(can("createUser", person("admin"))).toBe(true);
  });

  it("hides nothing without a known session: the server decides", () => {
    expect(roleOf(null)).toBe("admin");
    expect(can("createUser", null)).toBe(true);
  });
});

describe("approve by risk (D-329)", () => {
  const plan = (risk: Plan["risk"]) => ({ risk }) as Plan;
  it("apply up to disruptive, destructive only admin", () => {
    expect(canApprove(plan("reversible"), person("apply"))).toBe(true);
    expect(canApprove(plan("disruptive"), person("apply"))).toBe(true);
    expect(canApprove(plan("destructive"), person("apply"))).toBe(false);
    expect(canApprove(plan("destructive"), person("admin"))).toBe(true);
    expect(canApprove(plan("read"), person("plan"))).toBe(false);
  });
  it("an apply token keeps its max_risk", () => {
    const tok = { ...person("apply"), principal: "token:t", max_risk: "reversible" as const };
    expect(canTakeRisk("reversible", tok)).toBe(true);
    expect(canTakeRisk("disruptive", tok)).toBe(false);
  });
});

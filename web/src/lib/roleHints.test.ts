// T-171: the role hints match the minimum roles of the contract (D-342).
import { describe, expect, it } from "vitest";
import roles from "./roles.gen.json";
import { ROLE_HINTS } from "./roleHints";
import type { Operation, Role } from "./session.svelte";

const minRole = roles as Record<Operation, Role | "none">;

describe("role hints", () => {
  it.each(Object.entries(ROLE_HINTS))("%s lists only operations of its own role", (role, h) => {
    for (const op of h.ops) expect([op, minRole[op]]).toEqual([op, role]);
  });

  it("every operation above read is in the hint of its role", () => {
    for (const [op, need] of Object.entries(minRole)) {
      if (need === "none" || need === "read") continue;
      expect([op, ROLE_HINTS[need].ops.includes(op as Operation)]).toEqual([op, true]);
    }
  });

  it("starting a backup is apply, not admin (review of #270)", () => {
    expect(ROLE_HINTS.apply.text).toContain("starts a backup now");
    expect(ROLE_HINTS.admin.text).not.toMatch(/backup/i);
  });
});

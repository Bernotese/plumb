// The role of the signed-in person (D-329, D-342) for role-aware buttons
// (T-154). This is comfort only: the server checks every call (T-147); a
// hidden button never protects anything.
//
// The minimum role of each operation comes from api/openapi.yaml at
// `npm run gen` (scripts/gen-examples.mjs → roles.gen.json), so the UI
// keeps no second table of rights.
import roles from "./roles.gen.json";
import type { Plan, Risk, Session } from "./types";

export type Role = "read" | "plan" | "apply" | "admin";
export type Operation = keyof typeof roles;

const ORDER: readonly Role[] = ["read", "plan", "apply", "admin"];

/** The current session; App.svelte sets it at login and clears it at logout. */
export const me = $state<{ session: Session | null }>({ session: null });

/**
 * The role of a session. Without a known session (a screen outside the app
 * shell, as in screen tests) the UI hides nothing: the server decides.
 */
export function roleOf(s: Session | null): Role {
  return s ? s.tier : "admin";
}

function atLeast(role: Role, need: Role | "none"): boolean {
  if (need === "none") return true;
  return ORDER.indexOf(role) >= ORDER.indexOf(need);
}

/** Whether the role may call the operation (D-342). Unknown operations: no. */
export function can(op: Operation, s: Session | null = me.session): boolean {
  const need = roles[op] as Role | "none" | undefined;
  return need !== undefined && atLeast(roleOf(s), need);
}

/**
 * Approve and `ignore_once` inside the operation (ARCHITECTURE §17): `apply`
 * up to `disruptive`, `destructive` only `admin`. An apply token also has its
 * `max_risk`.
 */
export function canTakeRisk(risk: Risk, s: Session | null = me.session): boolean {
  const role = roleOf(s);
  if (!can("approvePlan", s)) return false;
  if (role === "admin") return true;
  if (risk === "destructive") return false;
  if (s?.max_risk === "reversible" && risk === "disruptive") return false;
  return true;
}

/** Whether the person may approve this pending Plan (role and risk). */
export function canApprove(plan: Plan, s: Session | null = me.session): boolean {
  return canTakeRisk(plan.risk, s);
}

/** A short text for a hidden or disabled write action. */
export function roleNote(need: Role, s: Session | null = me.session): string {
  const role = roleOf(s);
  return `Your role is ${role}. This needs the role ${need} or higher.`;
}

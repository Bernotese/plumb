// The role hints of Settings → Users (T-171). Each hint lists the
// operations it promises; roleHints.test.ts checks them against the
// minimum roles from api/openapi.yaml (roles.gen.json, D-342), so the text
// cannot drift from the rights again (review of #270: "backup now" is
// apply, not admin).
import type { Operation, Role } from "./session.svelte";

export interface RoleHint {
  text: string;
  /** Every operation whose minimum role is this role (none for read). */
  ops: Operation[];
}

export const ROLE_HINTS: Record<Role, RoleHint> = {
  read: { text: "Sees everything. Changes nothing.", ops: [] },
  plan: {
    text: "Also proposes changes (plans, also image builds, checksum refreshes, rollbacks and agent updates), comments on and resolves issues, drops plans.",
    ops: [
      "createPlan",
      "buildImage",
      "refreshImagePin",
      "rollbackRevision",
      "updateTargetAgent",
      "reenrollTarget",
      "commentIssue",
      "resolveIssue",
      "discardPlan",
    ],
  },
  apply: {
    text: "Also approves plans up to the risk disruptive, ignores drift once, and starts a backup now.",
    ops: ["approvePlan", "createBackup"],
  },
  admin: {
    text: "Everything: destructive plans, users, API tokens, secrets, an export with secret values, import, and the confirm after a restore.",
    ops: [
      "listUsers",
      "createUser",
      "changeUser",
      "resetUserPassword",
      "listTokens",
      "createToken",
      "enableToken",
      "revokeToken",
      "putSecret",
      "deleteSecret",
      "exportStateWithSecrets",
      "importState",
      "confirmRestore",
    ],
  },
};

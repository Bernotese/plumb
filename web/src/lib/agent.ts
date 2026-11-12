// The agent of a Target: version, update state and the gate refusal
// (T-097; D-257 … D-259). Pure functions, so the screens stay thin.
import { ApiError } from "./api";
import type { TargetView } from "./types";
import type { Tone } from "./view";

type Status = TargetView["status"];
export type AgentUpdate = NonNullable<Status["agent_update"]>;

export interface AgentState {
  /** Short badge text; empty when there is nothing to say (current agent). */
  label: string;
  tone: Tone;
  /** One sentence for the Target page. */
  text: string;
  /** "Propose agent update" makes sense: the server takes the request. */
  canUpdate: boolean;
}

const STATES: Record<AgentUpdate, AgentState> = {
  none: {
    label: "",
    tone: "ok",
    text: "The agent is current: it runs the binary that this controller serves.",
    canUpdate: false,
  },
  available: {
    label: "Update available",
    tone: "info",
    text: "This controller serves a newer agent. The current agent can still apply the desired state.",
    canUpdate: true,
  },
  required: {
    label: "Update required",
    tone: "danger",
    text: "The desired state needs things this agent does not know. Plumb refuses every plan for this Target until the agent is updated.",
    canUpdate: true,
  },
  manual: {
    label: "Manual update",
    tone: "warn",
    text: "This agent is older than slice 3 and cannot update itself. Run the one-time install in the guest as root: stop plumb-agent, then run plumb-agent-install. Or re-create the Target.",
    canUpdate: false,
  },
  unknown: {
    label: "",
    tone: "muted",
    text: "The agent has not said hello yet, so its version is not known.",
    canUpdate: false,
  },
};

/** The state of the agent of a Target; undefined on an older controller without the field. */
export function agentState(status: Status): AgentState | undefined {
  const u = status.agent_update;
  return u ? STATES[u] : undefined;
}

export const REVERT_REASON: Record<"crash-loop" | "no-hello", string> = {
  "crash-loop": "the new binary did not stay up",
  "no-hello": "the new binary had no successful hello within 5 minutes",
};

/** What a missing capability means: "unit" is a resource type, "file.acl" an attribute. */
export function missingText(m: string): string {
  const [type, attr] = m.split(/\.(.*)/s);
  return attr ? `attribute ${attr} of ${type}` : `resource type ${type}`;
}

export interface Outdated {
  detail: string;
  targets: string[];
  missing: string[];
}

/** The 409 `agent-outdated` refusal (D-259), or undefined for any other error. */
export function outdatedOf(err: unknown): Outdated | undefined {
  if (!(err instanceof ApiError)) return undefined;
  const p = err.problem;
  if (!p.type.endsWith(":agent-outdated")) return undefined;
  return {
    detail: p.detail ?? "Update the agent first.",
    targets: p.targets ?? [],
    missing: p.missing ?? [],
  };
}

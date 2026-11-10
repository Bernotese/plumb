// Display rules shared by the screens. Pure functions, tested in view.test.ts.
import type { Change, Risk, TargetView } from "./types";

export type Tone = "ok" | "warn" | "danger" | "info" | "muted";

/** The four user-facing target states of the card, plus the rare ones. */
export function targetState(t: TargetView): { label: string; tone: Tone; detail: string } {
  const s = t.status;
  if (s.agent === "offline") {
    return {
      label: "Offline",
      tone: "muted",
      detail: "The agent has not polled in the last 90 s.",
    };
  }
  switch (s.phase) {
    case "ready":
      return { label: "In Plumb", tone: "ok", detail: "The guest matches the desired state." };
    case "drifted":
      return {
        label: "Out of Plumb",
        tone: "warn",
        detail: `${s.open_issues} open issue${s.open_issues === 1 ? "" : "s"}.`,
      };
    case "applying":
    case "provisioning":
    case "enrolling":
      return { label: "Applying", tone: "info", detail: `Phase: ${s.phase}.` };
    case "pending":
      return { label: "Pending", tone: "muted", detail: "No guest yet." };
    case "error":
      return { label: "Error", tone: "danger", detail: "The last run failed." };
  }
}

export const riskTone: Record<Risk, Tone> = {
  read: "muted",
  reversible: "ok",
  disruptive: "warn",
  destructive: "danger",
};

export const riskOrder: Risk[] = ["read", "reversible", "disruptive", "destructive"];

export function maxRisk(risks: Risk[]): Risk {
  return risks.reduce<Risk>(
    (hi, r) => (riskOrder.indexOf(r) > riskOrder.indexOf(hi) ? r : hi),
    "read",
  );
}

export function isSensitive(c: Change): c is Extract<Change, { sensitive: true }> {
  return "sensitive" in c && c.sensitive === true;
}

/** Text of one side of a change. A missing value means "not there". */
export function valueText(v: unknown): string {
  if (v === undefined || v === null) return "(not there)";
  if (typeof v === "string") return v === "" ? '""' : v;
  if (Array.isArray(v)) return v.length === 0 ? "(empty list)" : v.map(String).join("\n");
  return JSON.stringify(v);
}

/** One change as text for display: sensitive values are never shown; a restart has no values. */
export function changeView(c: Change): {
  attr: string;
  from: string;
  to: string;
  hidden: boolean;
  restart: boolean;
} {
  if (isSensitive(c)) return { attr: c.attr, from: "", to: "", hidden: true, restart: false };
  if (c.attr === "restart" && c.from === undefined && c.to === undefined) {
    return { attr: c.attr, from: "", to: "", hidden: false, restart: true };
  }
  return {
    attr: c.attr,
    from: valueText(c.from),
    to: valueText(c.to),
    hidden: false,
    restart: false,
  };
}

export function formatTime(iso: string | null | undefined): string {
  if (!iso) return "–";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

/** Guest settings that cannot change after create in the first slice (D-025). */
export const CREATE_ONLY = ["ip", "gateway", "user", "ssh_keys", "disk"] as const;

/** Applies one run.step event to a run snapshot; false when it does not fit. */
export function applyStepEvent(
  run: { id: number; targets: { target: string; steps: { resource: string; status: string }[] }[] },
  e: { type: string; subject: string; data: Record<string, unknown> },
): boolean {
  if (e.type !== "run.step" || e.subject !== `run:${run.id}`) return false;
  const d = e.data as { target?: string; resource?: string; status?: string };
  const step = run.targets
    .find((t) => t.target === d.target)
    ?.steps.find((s) => s.resource === d.resource);
  if (!step || !d.status) return false;
  step.status = d.status;
  return true;
}

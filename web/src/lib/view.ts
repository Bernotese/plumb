// Display rules shared by the screens. Pure functions, tested in view.test.ts.
import type { Change, Issue, Plan, Risk, TargetView } from "./types";

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
    case "restart_needed":
      return {
        label: "Restart needed",
        tone: "warn",
        detail: `${s.waiting.length} value${s.waiting.length === 1 ? "" : "s"} set in Proxmox, waiting for a stop and start of the VM.`,
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
export function changeView(
  c: Change,
  resource?: string,
): {
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
  if (resource === GUEST_CONFIG && c.attr === "allow_restart") {
    return {
      attr: ALLOW_RESTART_LABEL,
      from: c.from === true ? "yes" : "no",
      to: c.to === true ? "yes" : "no",
      hidden: false,
      restart: false,
    };
  }
  return {
    attr: c.attr,
    from: valueText(c.from),
    to: valueText(c.to),
    hidden: false,
    restart: false,
  };
}

// --- T-056: pending values, allow_restart, "restart needed" (D-190, D-194, D-195) ---

export const GUEST_CONFIG = "guest:config";

/** The label of spec.allow_restart (D-196). */
export const ALLOW_RESTART_LABEL = "Plumb may restart this VM";

/** The help text of the allow_restart checkbox (card step 10). */
export const ALLOW_RESTART_HELP =
  "Off: a change of cores or memory waits until you stop and start the VM. On: the Plan shows the restart, and the Run does it.";

/** The text of the restart change on guest:config; the 2 minutes are pinned in Go. */
export const RESTART_TEXT =
  "Restart of the VM if Proxmox cannot apply the change live: clean shutdown, hard power-off after 2 minutes, then start.";

/** The refusal of a re-enrollment of a running VM with allow_restart off (D-194). */
export const REENROLL_REFUSED =
  "Re-enrollment restarts the VM, and this Target does not allow a restart by a Run. Stop the VM in Proxmox and re-enroll again (the Run starts it). Or set allow_restart first (its own Plan), then re-enroll.";

/** Proxmox settings that Plumb never changes after create (cloud-init inputs, D-185). */
const CREATE_ONLY_GUEST = ["kind", "image", "disk", "ip", "gateway", "user", "ssh_keys"];

/** The tags and the sentences that explain one change (D-190, D-194). */
export function changeNotes(c: Change, resource?: string): { tags: string[]; notes: string[] } {
  const tags: string[] = [];
  const notes: string[] = [];
  if (resource !== GUEST_CONFIG || isSensitive(c)) return { tags, notes };
  if (c.attr === "restart" && c.from === undefined && c.to === undefined) {
    return { tags: ["restart"], notes: [RESTART_TEXT] };
  }
  if (c.attr === "allow_restart") {
    return { tags, notes: ["A rule of Plumb: nothing changes in Proxmox."] };
  }
  const from = valueText(c.from);
  const to = valueText(c.to);
  if (c.pending) {
    tags.push("pending");
    notes.push(
      `Set in Proxmox but not active. The VM uses ${to}. ${from} becomes active at the next stop and start of the VM. A reboot inside the guest is not enough.`,
    );
    if (CREATE_ONLY_GUEST.includes(c.attr)) {
      notes.push(
        "Plumb does not revert this value. Remove it in Proxmox: use Revert on the pending value, or `qm set <vmid> --revert <key>`.",
      );
    }
  }
  if (c.also_pending !== undefined && c.also_pending !== null) {
    notes.push(`Also pending: ${valueText(c.also_pending)}.`);
  }
  if (c.waits_for_restart) {
    tags.push("after restart");
    if (c.risk === "read") {
      notes.push(
        `${to} is set in Proxmox already and waits. The VM uses ${from} until the restart of this Plan.`,
      );
    } else {
      notes.push(
        `Plumb writes ${to} to Proxmox. The VM keeps ${from} until its next stop and start. That can be a restart that nobody plans (host maintenance, HA). This Run does not restart the VM (allow_restart is off). A reboot inside the guest is not enough.`,
      );
    }
  }
  return { tags, notes };
}

type Waiting = TargetView["status"]["waiting"][number];

/** A waiting value with its unit. */
export function waitingValue(attr: string, v: number): string {
  return attr === "memory" ? `${v} MiB` : String(v);
}

/** How long a value waits, short: "5 min", "3 h", "2 days". */
export function ageText(since: string, now: Date = new Date()): string {
  const ms = now.getTime() - new Date(since).getTime();
  if (!Number.isFinite(ms) || ms < 60_000) return "under 1 min";
  const min = Math.floor(ms / 60_000);
  if (min < 60) return `${min} min`;
  const h = Math.floor(min / 60);
  if (h < 48) return `${h} h`;
  return `${Math.floor(h / 24)} days`;
}

/** The oldest since of the list, or null for an empty list. */
export function waitingSince(waiting: Waiting[]): string | null {
  return waiting.reduce<string | null>((a, w) => (a === null || w.since < a ? w.since : a), null);
}

/** One sentence per waiting value (Target page, D-195). */
export function waitingLines(waiting: Waiting[]): string[] {
  return waiting.map(
    (w) =>
      `${w.attr}: the VM uses ${waitingValue(w.attr, w.from)}. ${waitingValue(w.attr, w.to)} is set and becomes active at the next stop and start of the VM.`,
  );
}

/** What the user can do while values wait (Target page, D-195). */
export const WAITING_ACTIONS = `Stop and start the VM in Proxmox; a reboot inside the guest is not enough. Or switch on '${ALLOW_RESTART_LABEL}' and make a Plan.`;

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

/**
 * Why only a person can approve a Plan (D-099), as short sentences:
 * "gives secret app-db to web1", "changes an ignore rule".
 */
export function humanOnlyReasons(plan: Plan): string[] {
  const out: string[] = [];
  for (const reason of plan.human_only ?? []) {
    if (reason === "rule-change") {
      out.push("It adds, changes or removes an ignore rule.");
      continue;
    }
    // The targets that get a secret in a changed resource of this Plan.
    const given = plan.targets.filter(
      (t) =>
        (t.secrets?.length ?? 0) > 0 &&
        t.resources.some((r) => r.changes.some((c) => isSensitive(c))),
    );
    if (given.length === 0) {
      out.push("It gives a secret to a target that did not get it before.");
    }
    // The contract does not say which binding is new, so the text names
    // what the target uses, not what it gets (review #48).
    for (const t of given) {
      const names = (t.secrets ?? []).map((s) => s.name);
      out.push(
        `It gives a secret to ${t.target} that ${t.target} did not get before. ${t.target} uses: ${names.join(", ")}.`,
      );
    }
  }
  return out;
}

// --- T-071: addresses reported by the guest (D-212 … D-216) ---

export const GUEST_ADDRESS = "guest:address";

/** The mark next to every address that the agent in the guest reports (D-216). */
export const REPORTED_MARK = "reported by the guest";

/** Why the mark is there: root in the guest can send any list. */
export const REPORTED_TITLE =
  "The agent in the guest sends this list. Plumb shows it only and never connects to it.";

/** The hint on a guest:address Issue (D-192). */
export const ADDRESS_HINT =
  "If the image is a stock Ubuntu 26.04, restart the guest once: it takes a static address only from the second boot (D-192). Else check the network setup in the guest.";

type Status = TargetView["status"];

/**
 * The reported list for display, or null when Plumb has no list
 * (agent offline, old agent: reported_at is null, D-217).
 */
export function reportedAddrs(s: Status): { ips: string[]; more: boolean } | null {
  if (!s.reported_at) return null;
  return { ips: s.reported_ips ?? [], more: s.reported_ips_more === true };
}

/** "192.0.2.107, fd00::41 + more", or "no address". */
export function reportedText(r: { ips: string[]; more: boolean }): string {
  if (r.ips.length === 0) return r.more ? "+ more" : "no address";
  return r.ips.join(", ") + (r.more ? " + more" : "");
}

/** The list of a guest:address change: "192.0.2.107" or "no address". */
function listText(v: unknown): string {
  const list = Array.isArray(v) ? v.map(String) : [];
  return list.length ? list.join(", ") : "no address";
}

/** The text of a guest:address Issue (D-215). */
export function addressIssueText(issue: Issue): string {
  const c = issue.changes.find((x) => x.attr === "ip");
  if (!c || isSensitive(c)) return "The guest does not use its configured address.";
  return `The guest does not use the configured address ${valueText(c.to)}. It reports: ${listText(c.from)}.`;
}

/** The mismatch line of the Target page: "Configured: X — the guest reports Y". */
export function mismatchLine(issue: Issue): string {
  const c = issue.changes.find((x) => x.attr === "ip");
  if (!c || isSensitive(c)) return "The guest does not use its configured address.";
  return `Configured: ${valueText(c.to)} — the guest reports ${listText(c.from)}`;
}

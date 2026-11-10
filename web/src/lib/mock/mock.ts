// In-memory mock of the Plumb API for T-006. Its data comes from the
// examples of api/openapi.yaml; it computes plans, runs and events the way
// the server does (ARCHITECTURE §3, §4), in a much simpler form.
import { ApiError } from "../api";
import type { PlumbApi } from "../client";
import type {
  BlockDoc,
  BlockView,
  ImageView,
  Change,
  Issue,
  IssueAction,
  Plan,
  PlumbEvent,
  Problem,
  Proposal,
  ResourcePlan,
  Risk,
  Run,
  Session,
  TargetDoc,
  TargetView,
} from "../types";
import { CREATE_ONLY, maxRisk } from "../view";
import { example, exampleItems } from "./examples";

/** Login of the mock (shown on the login screen in dev mode). */
export const MOCK_USER = { username: "admin", password: "correct-horse-battery-staple" };

export interface MockOptions {
  /** Delay between run steps in ms (default 600). */
  stepDelayMs?: number;
  /** Start signed in, password already changed (for screenshots and dev). */
  signedIn?: boolean;
  /** Start signed in at the first login: the password change is due. */
  firstLogin?: boolean;
}

export interface MockApi extends PlumbApi {
  /** Resolves when no simulated run is active (tests). */
  idle(): Promise<void>;
}

type Rendered = { id: string; type: string; attrs: Record<string, unknown> };

const now = () => new Date().toISOString();
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

function problem(
  type: string,
  title: string,
  status: number,
  detail?: string,
  extra?: Partial<Problem>,
): ApiError {
  return new ApiError({
    type: `urn:plumb:problem:${type}`,
    title,
    status,
    ...(detail ? { detail } : {}),
    ...extra,
  });
}

/** Replaces `{{ .name }}` with the parameter value (Go text/template subset). */
export function renderValue(v: unknown, params: Record<string, unknown>): unknown {
  if (typeof v === "string") {
    return v.replace(/\{\{\s*\.(\w+)\s*\}\}/g, (_, k: string) => {
      const p = params[k];
      return Array.isArray(p) ? p.join(" ") : p === undefined ? "" : String(p);
    });
  }
  if (Array.isArray(v)) return v.map((e) => renderValue(e, params));
  return v;
}

const keyAttr: Record<string, string> = {
  file: "path",
  directory: "path",
  package: "name",
  service: "name",
  user: "name",
};

/** Renders the Blocks of a Target into a flat resource list. */
export function render(spec: TargetDoc["spec"], blocks: Map<string, BlockDoc>): Rendered[] {
  const out: Rendered[] = [];
  for (const use of spec.blocks ?? []) {
    const block = blocks.get(use.use);
    if (!block)
      throw problem("validation", "Validation failed", 422, `Block "${use.use}" does not exist.`);
    const params: Record<string, unknown> = {};
    for (const [k, s] of Object.entries(block.spec.params?.properties ?? {})) {
      const d = (s as { default?: unknown }).default;
      if (d !== undefined) params[k] = d;
    }
    Object.assign(params, use.params ?? {});
    for (const entry of block.spec.resources) {
      const [type, rawAttrs] = Object.entries(entry)[0] ?? [];
      if (!type || !rawAttrs) continue;
      const attrs: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(rawAttrs)) attrs[k] = renderValue(v, params);
      out.push({ id: `${type}:${String(attrs[keyAttr[type] ?? "name"])}`, type, attrs });
    }
  }
  return out;
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

function resourcePlan(
  resource: string,
  action: ResourcePlan["action"],
  changes: Change[],
): ResourcePlan {
  return { resource, action, risk: maxRisk(changes.map((c) => c.risk)), changes };
}

/** The resource plans of one Target: from the old spec (or none) to the new one. */
export function planTarget(
  old: TargetDoc["spec"] | undefined,
  next: TargetDoc["spec"],
  blocks: Map<string, BlockDoc>,
): ResourcePlan[] {
  const out: ResourcePlan[] = [];
  const g = next.guest as Record<string, unknown>;
  if (!old) {
    out.push(
      resourcePlan(
        "guest:config",
        "create",
        Object.entries(g).map(([attr, to]) => ({ attr, to, risk: "reversible" as Risk })),
      ),
    );
  } else {
    const og = old.guest as Record<string, unknown>;
    const changes: Change[] = [];
    for (const attr of new Set([...Object.keys(og), ...Object.keys(g)])) {
      if (same(og[attr], g[attr])) continue;
      if ((CREATE_ONLY as readonly string[]).includes(attr)) {
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [
            {
              pointer: `/put/0/spec/guest/${attr}`,
              message: `${attr} is create-only in this version. Create a new target instead.`,
            },
          ],
        });
      }
      changes.push({ attr, from: og[attr], to: g[attr], risk: "disruptive" });
    }
    if (changes.length) out.push(resourcePlan("guest:config", "update", changes));
  }

  const before = new Map((old ? render(old, blocks) : []).map((r) => [r.id, r]));
  const after = render(next, blocks);
  const updated = new Set<string>();
  for (const r of after) {
    const prev = before.get(r.id);
    if (!prev) {
      const changes: Change[] = [
        { attr: "state", from: "absent", to: "present", risk: "reversible" },
      ];
      for (const [attr, to] of Object.entries(r.attrs))
        changes.push({ attr, to, risk: "reversible" });
      out.push(resourcePlan(r.id, "create", changes));
      continue;
    }
    const changes: Change[] = [];
    for (const attr of new Set([...Object.keys(prev.attrs), ...Object.keys(r.attrs)])) {
      if (!same(prev.attrs[attr], r.attrs[attr])) {
        changes.push({ attr, from: prev.attrs[attr], to: r.attrs[attr], risk: "reversible" });
      }
    }
    if (changes.length) {
      updated.add(r.id);
      out.push(resourcePlan(r.id, "update", changes));
    }
  }
  // A service restarts after a changed restart_on resource (D-036).
  for (const r of after) {
    const on = (r.attrs["restart_on"] as string[] | undefined) ?? [];
    if (r.type === "service" && before.has(r.id) && on.some((id) => updated.has(id))) {
      out.push(resourcePlan(r.id, "restart", [{ attr: "restart", risk: "disruptive" }]));
    }
  }
  const nextIds = new Set(after.map((r) => r.id));
  for (const id of before.keys()) {
    if (!nextIds.has(id)) {
      out.push(
        resourcePlan(id, "delete", [
          { attr: "state", from: "present", to: "absent", risk: "destructive" },
        ]),
      );
    }
  }
  return out;
}

export function createMockApi(opts: MockOptions = {}): MockApi {
  const stepDelay = opts.stepDelayMs ?? 600;
  let session: Session | null = null;
  let password = MOCK_USER.password;
  let mustChange = true;
  if (opts.signedIn) {
    session = { ...example("login", 200), must_change_password: false };
    mustChange = false;
  } else if (opts.firstLogin) {
    session = example("login", 200);
  }

  const images = exampleItems<ImageView>(example("listImages", 200));
  const blockViews = exampleItems<BlockView>(example("listBlocks", 200));
  const blocks = new Map(blockViews.map((b) => [b.document.name, b.document]));
  const targets = new Map(
    exampleItems<TargetView>(example("listTargets", 200)).map((t) => [t.document.name, t]),
  );

  const plans = new Map<number, Plan>();
  for (const p of [
    ...exampleItems<Plan>(example("listPlans", 200)),
    example("getPlan", 200),
    example("reenrollTarget", 201),
    example("resolveIssue", 201),
  ]) {
    if (!plans.has(p.id)) plans.set(p.id, p);
  }
  const runs = new Map<number, Run>();
  for (const r of [...exampleItems<Run>(example("listRuns", 200)), example("getRun", 200)])
    runs.set(r.id, r);
  const issues = new Map<number, Issue>();
  for (const i of [...exampleItems<Issue>(example("listIssues", 200)), example("getIssue", 200)])
    issues.set(i.id, i);
  // Plan 15 (example) resolves issue 7.
  const i7 = issues.get(7);
  const p15 = plans.get(15);
  if (i7 && p15?.issue_id === 7) {
    i7.status = "resolving";
    i7.plan_id = 15;
    i7.actions = [];
  }

  let head = Math.max(
    ...[...targets.values()].map((t) => t.revision),
    ...[...plans.values()].map((p) => p.revision ?? 0),
  );
  let nextPlan = Math.max(...plans.keys()) + 1;
  let nextRun = Math.max(...runs.keys()) + 1;
  let seq = 1100;
  const log: PlumbEvent[] = [];
  const listeners = new Set<(e: PlumbEvent) => void>();
  const active = new Set<Promise<void>>();

  function emit(
    type: PlumbEvent["type"],
    subject: string,
    data: Record<string, unknown>,
    actor = session?.principal ?? "system",
  ) {
    const e: PlumbEvent = { seq: ++seq, type, time: now(), subject, actor, data };
    log.push(e);
    for (const l of listeners) l(e);
  }

  function auth(): Session {
    if (!session) throw new ApiError(example("getSession", 401));
    if (mustChange) {
      throw problem(
        "password-change-required",
        "Password change required",
        403,
        "Change your password first.",
      );
    }
    return session;
  }

  function findPlan(id: number): Plan {
    const p = plans.get(id);
    if (!p) throw problem("not-found", "Not found", 404, `Plan ${id} does not exist.`);
    return p;
  }

  function newPlan(
    fields: Pick<Plan, "origin" | "issue_id" | "reason" | "targets" | "warnings">,
  ): Plan {
    const s = auth();
    const all = fields.targets.flatMap((t) =>
      t.resources.flatMap((r) => r.changes.map((c) => c.risk)),
    );
    const p: Plan = {
      id: nextPlan++,
      status: "pending",
      risk: maxRisk(all),
      author: s.principal,
      created_at: now(),
      base_revision: head,
      approved_by: null,
      approved_at: null,
      revision: null,
      run_id: null,
      ...fields,
    };
    plans.set(p.id, p);
    emit("plan.created", `plan:${p.id}`, { risk: p.risk });
    return structuredClone(p);
  }

  async function simulate(run: Run, plan: Plan, apply: () => void) {
    emit("run.started", `run:${run.id}`, { plan_id: plan.id });
    for (const tr of run.targets) {
      tr.status = "running";
      const target = targets.get(tr.target);
      if (target) target.status.phase = "applying";
      for (const step of tr.steps) {
        await sleep(stepDelay);
        step.status = "running";
        step.started_at = now();
        emit("run.step", `run:${run.id}`, {
          target: tr.target,
          resource: step.resource,
          status: "running",
        });
        await sleep(stepDelay);
        step.status = step.changes.length === 0 ? "unchanged" : "applied";
        step.finished_at = now();
        emit("run.step", `run:${run.id}`, {
          target: tr.target,
          resource: step.resource,
          status: step.status,
        });
      }
      tr.status = "succeeded";
      if (target) target.status.phase = target.status.open_issues > 0 ? "drifted" : "ready";
    }
    apply();
    run.status = "succeeded";
    run.finished_at = now();
    emit("run.finished", `run:${run.id}`, { status: run.status });
  }

  const api: MockApi = {
    async session() {
      if (!session) throw new ApiError(example("getSession", 401));
      return { ...session, must_change_password: mustChange };
    },
    async login(username, pw) {
      if (username !== MOCK_USER.username || pw !== password)
        throw new ApiError(example("login", 401));
      session = example("login", 200);
      session.must_change_password = mustChange;
      return structuredClone(session);
    },
    async logout() {
      session = null;
    },
    async changePassword(current, next) {
      if (!session) throw new ApiError(example("getSession", 401));
      if (current !== password) {
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [{ pointer: "/current_password", message: "The current password is wrong." }],
        });
      }
      if (next.length < 12) {
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [{ pointer: "/new_password", message: "Use at least 12 characters." }],
        });
      }
      password = next;
      mustChange = false;
    },
    async listImages() {
      auth();
      return structuredClone(images);
    },
    async listBlocks() {
      auth();
      return structuredClone(blockViews);
    },
    async listTargets() {
      auth();
      return structuredClone([...targets.values()]);
    },
    async getTarget(name) {
      auth();
      const t = targets.get(name);
      if (!t) throw new ApiError(example("getTarget", 404));
      return structuredClone(t);
    },
    async createPlan(proposal: Proposal) {
      auth();
      if (!proposal.reason.trim()) {
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [{ pointer: "/reason", message: "A reason is required." }],
        });
      }
      const planTargets = [];
      for (const doc of proposal.put ?? []) {
        if (doc.kind !== "Target") continue;
        const t = doc as TargetDoc;
        if (!images.some((i) => i.document.name === t.spec.image)) {
          throw problem(
            "validation",
            "Validation failed",
            422,
            `Image "${t.spec.image}" does not exist.`,
          );
        }
        const resources = planTarget(targets.get(t.name)?.document.spec, t.spec, blocks);
        planTargets.push({ target: t.name, resources });
      }
      const p = newPlan({
        origin: "proposal",
        issue_id: null,
        reason: proposal.reason,
        targets: planTargets,
        warnings: [],
      });
      pendingDocs.set(p.id, proposal);
      return p;
    },
    async getPlan(id) {
      auth();
      return structuredClone(findPlan(id));
    },
    async approvePlan(id) {
      const s = auth();
      const p = findPlan(id);
      if (p.status === "stale" || (p.status === "pending" && p.base_revision !== head)) {
        if (p.status !== "stale") {
          p.status = "stale";
          emit("plan.stale", `plan:${p.id}`, {});
        }
        throw new ApiError(example("approvePlan", 409));
      }
      if (p.status !== "pending")
        throw problem("conflict", "Plan is not pending", 409, `Plan ${id} is ${p.status}.`);
      head += 1;
      p.status = "approved";
      p.approved_by = s.principal;
      p.approved_at = now();
      p.revision = head;
      const run: Run = {
        id: nextRun++,
        plan_id: p.id,
        revision: head,
        status: "running",
        started_at: now(),
        finished_at: null,
        targets: p.targets.map((t) => ({
          target: t.target,
          status: "running",
          steps: t.resources.map((r) => ({
            resource: r.resource,
            action: r.action,
            status: "pending",
            changes: r.changes,
            output: "",
            started_at: null,
            finished_at: null,
          })),
        })),
      };
      p.run_id = run.id;
      runs.set(run.id, run);
      emit("plan.approved", `plan:${p.id}`, { run_id: run.id });
      // Other pending plans were made against the old head: they are stale now.
      for (const other of plans.values()) {
        if (other.status === "pending" && other.base_revision !== head) {
          other.status = "stale";
          emit("plan.stale", `plan:${other.id}`, {});
        }
      }
      const proposal = pendingDocs.get(p.id);
      const job = simulate(run, p, () => {
        for (const doc of proposal?.put ?? []) {
          if (doc.kind !== "Target") continue;
          const t = doc as TargetDoc;
          const prev = targets.get(t.name);
          const view: TargetView = {
            revision: head,
            document: t,
            status: prev?.status ?? {
              phase: "ready",
              agent: "online",
              power: "running",
              last_seen: now(),
              guest_id: String(9000 + targets.size + 1),
              ips: [],
              applied_revision: head,
              open_issues: 0,
              last_run_id: run.id,
            },
          };
          view.status.last_run_id = run.id;
          view.status.applied_revision = head;
          targets.set(t.name, view);
        }
        const issue = p.issue_id ? issues.get(p.issue_id) : undefined;
        if (issue) {
          issue.status = "resolved";
          issue.resolution = {
            action: p.origin === "accept" ? "accept" : "revert",
            by: s.principal,
            at: now(),
          };
          const target = targets.get(issue.target);
          if (target) {
            target.status.open_issues = Math.max(0, target.status.open_issues - 1);
            if (target.status.open_issues === 0 && target.status.phase === "drifted")
              target.status.phase = "ready";
          }
          emit("issue.resolved", `issue:${issue.id}`, { plan_id: p.id });
        }
      });
      active.add(job);
      void job.finally(() => active.delete(job));
      return structuredClone(run);
    },
    async getRun(id) {
      auth();
      const r = runs.get(id);
      if (!r) throw new ApiError(example("getRun", 404));
      return structuredClone(r);
    },
    async listIssues() {
      auth();
      return structuredClone([...issues.values()].sort((a, b) => b.id - a.id));
    },
    async getIssue(id) {
      auth();
      const i = issues.get(id);
      if (!i) throw new ApiError(example("getIssue", 404));
      return structuredClone(i);
    },
    async resolveIssue(id, action: IssueAction, reason) {
      auth();
      const issue = issues.get(id);
      if (!issue) throw new ApiError(example("getIssue", 404));
      if (issue.status !== "open" || !issue.actions.includes(action)) {
        throw problem(
          "conflict",
          "Action not allowed",
          409,
          `Issue ${id} does not allow "${action}" now.`,
        );
      }
      const changes: Change[] =
        action === "revert"
          ? issue.changes
          : issue.changes.map((c) =>
              "sensitive" in c ? c : { attr: c.attr, from: c.to, to: c.from, risk: "read" as Risk },
            );
      const p = newPlan({
        origin: action,
        issue_id: id,
        reason:
          reason?.trim() || (action === "revert" ? "Revert drift." : "Accept the guest value."),
        targets: [
          { target: issue.target, resources: [resourcePlan(issue.resource, "update", changes)] },
        ],
        warnings:
          action === "accept"
            ? [
                "Accept writes the guest value into the Target as an override. The guest does not change.",
              ]
            : [],
      });
      issue.status = "resolving";
      issue.plan_id = p.id;
      issue.actions = [];
      emit("issue.updated", `issue:${id}`, { plan_id: p.id });
      return p;
    },
    events(since, onEvent) {
      for (const e of log) if (e.seq > since) onEvent(e);
      listeners.add(onEvent);
      return () => listeners.delete(onEvent);
    },
    async idle() {
      while (active.size) await Promise.all([...active]);
    },
  };
  const pendingDocs = new Map<number, Proposal>();
  return api;
}

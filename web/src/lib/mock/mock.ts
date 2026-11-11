// In-memory mock of the Plumb API for T-006. Its data comes from the
// examples of api/openapi.yaml; it computes plans, runs and events the way
// the server does (ARCHITECTURE §3, §4), in a much simpler form.
import { ApiError } from "../api";
import type { PlumbApi } from "../client";
import type {
  BlockDoc,
  BlockView,
  DocumentChange,
  ImageView,
  Change,
  Issue,
  IssueAction,
  Plan,
  PlumbEvent,
  Problem,
  Proposal,
  ResourcePlan,
  RevisionInfo,
  RuleDoc,
  RuleView,
  Risk,
  Run,
  ImportRequest,
  SecretInfo,
  Session,
  StateDocument,
  Token,
  TokenCreated,
  TargetDoc,
  TargetView,
} from "../types";
import { CREATE_ONLY, maxRisk, REENROLL_REFUSED } from "../view";
import { toYaml } from "../yaml";
import { secretName } from "../schemaForm";
import { example, exampleItems } from "./examples";

/** Banner of a mock build. */
export const MOCK_BANNER = "Demo data: this build uses a mock of the Plumb API.";

/** Login of the mock (shown on the login screen in dev mode). */
export const MOCK_USER = { username: "admin", password: "correct-horse-battery-staple" };

export interface MockOptions {
  /** Delay between run steps in ms (default 600). */
  stepDelayMs?: number;
  /** Start signed in, password already changed (for screenshots and dev). */
  signedIn?: boolean;
  /** Start signed in at the first login: the password change is due. */
  firstLogin?: boolean;
  /** Start as an API token of tier `plan` (no access to the settings, T-035). */
  token?: boolean;
}

export interface MockApi extends PlumbApi {
  /** Resolves when no simulated run is active (tests). */
  idle(): Promise<void>;
  /** Id of the demo Plan with nginx and two sites (T-049). */
  orderPlanId?: number;
}

type Rendered = { id: string; type: string; attrs: Record<string, unknown>; after?: string[] };

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

/** A secret reference renders as a marker, never as a value (D-088). */
export const marker = (name: string) => `\ue000${name}\ue001`;
const MARKER = /\ue000([^\ue001]*)\ue001/g;
const hasMarker = (v: unknown) => typeof v === "string" && v.includes("\ue000");

/** Secret names in the rendered attributes of a resource. */
export function secretsIn(attrs: Record<string, unknown>): string[] {
  const out = new Set<string>();
  for (const v of Object.values(attrs))
    if (typeof v === "string") for (const m of v.matchAll(MARKER)) out.add(m[1] ?? "");
  return [...out];
}

/** Replaces `{{ .name }}` with the parameter value (Go text/template subset). */
export function renderValue(v: unknown, params: Record<string, unknown>): unknown {
  if (typeof v === "string") {
    return v.replace(/\{\{\s*\.(\w+)\s*\}\}/g, (_, k: string) => {
      const p = params[k];
      const ref = secretName(p);
      if (ref !== undefined) return marker(ref);
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
  symlink: "path",
};

/**
 * Renders the Blocks of a Target into a flat resource list: Blocks, the
 * `restarts`, overrides, the `after` check (D-168, D-172).
 * The mock has no cycle check (T-044 plan, section 9).
 */
export function render(spec: TargetDoc["spec"], blocks: Map<string, BlockDoc>): Rendered[] {
  const out: Rendered[] = [];
  const uses: { block: BlockDoc; useId: string; ids: string[] }[] = [];
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
    const ids: string[] = [];
    for (const entry of block.spec.resources) {
      const [type, rawAttrs] = Object.entries(entry)[0] ?? [];
      if (!type || !rawAttrs) continue;
      const attrs: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(rawAttrs)) attrs[k] = renderValue(v, params);
      const id = `${type}:${String(attrs[keyAttr[type] ?? "name"])}`;
      const r: Rendered = { id, type, attrs };
      // The first resource of a use waits for the `after` ids of its Block (D-167).
      if (!ids.length && block.spec.after?.length) r.after = [...block.spec.after];
      ids.push(id);
      out.push(r);
    }
    uses.push({ block, useId: use.id ?? use.use, ids });
  }
  const byId = new Map(out.map((r) => [r.id, r]));
  // `restarts` adds the ids of the use to restart_on of each named service (D-172).
  for (const u of uses) {
    for (const svc of u.block.spec.restarts ?? []) {
      const s = byId.get(svc);
      if (!s)
        throw problem(
          "validation",
          "Validation failed",
          422,
          `Block "${u.block.name}" (use "${u.useId}") restarts ${svc}, but ${svc} is not on this target; add the Block that provides it`,
        );
      const on = [...((s.attrs["restart_on"] as string[] | undefined) ?? [])];
      for (const id of u.ids) if (id !== svc && !on.includes(id)) on.push(id);
      s.attrs["restart_on"] = on;
    }
  }
  // Target overrides replace rendered attributes ("Accept", raw mode, state: absent).
  for (const r of out) r.attrs = { ...r.attrs, ...(spec.overrides?.[r.id] ?? {}) };
  // An `after` id must be on the Target and not in the same use (D-168).
  for (const u of uses) {
    for (const id of u.block.spec.after ?? []) {
      const who = `Block "${u.block.name}" (use "${u.useId}") comes after ${id}`;
      if (u.ids.includes(id))
        throw problem(
          "validation",
          "Validation failed",
          422,
          `${who}, which is its own resource; resources of one Block already apply in the listed order`,
        );
      if (!byId.has(id))
        throw problem(
          "validation",
          "Validation failed",
          422,
          `${who}, but ${id} is not on this target; add the Block that provides it`,
        );
    }
  }
  return out;
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

/** A change; an attribute with a secret marker is sensitive and has no values (D-089). */
function change(attr: string, from: unknown, to: unknown, risk: Risk): Change {
  if (hasMarker(from) || hasMarker(to)) return { attr, sensitive: true, risk };
  const c: Change = { attr, risk };
  if (from !== undefined) c.from = from;
  if (to !== undefined) c.to = to;
  return c;
}

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
  /** The power state of the guest: only a running VM waits or restarts (D-194). */
  power: string = "running",
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
    // T-056 (D-194), for a running VM: off, cores and memory wait; on, the
    // Plan has the restart. A stopped VM takes them at its next start. The
    // setting itself is a Plan-only line.
    const allow = next.allow_restart === true;
    const cold =
      power === "running" ? changes.filter((c) => c.attr === "cores" || c.attr === "memory") : [];
    if (!allow) for (const c of cold) Object.assign(c, { waits_for_restart: true });
    else if (cold.length) changes.push({ attr: "restart", risk: "disruptive" });
    if ((old.allow_restart === true) !== allow) {
      changes.unshift({ attr: "allow_restart", from: !allow, to: allow, risk: "read" });
    }
    if (changes.length) out.push(resourcePlan("guest:config", "update", changes));
  }

  const before = new Map((old ? render(old, blocks) : []).map((r) => [r.id, r]));
  const after = render(next, blocks);
  for (const r of after) {
    const prev = before.get(r.id);
    // An explicit state: absent removes the resource on the guest (D-065).
    if (r.attrs["state"] === "absent") {
      if (prev?.attrs["state"] !== "absent") {
        out.push(
          resourcePlan(r.id, "delete", [
            { attr: "state", from: "present", to: "absent", risk: "destructive" },
          ]),
        );
      }
      continue;
    }
    if (!prev) {
      const changes: Change[] = [
        { attr: "state", from: "absent", to: "present", risk: "reversible" },
      ];
      for (const [attr, to] of Object.entries(r.attrs))
        changes.push(change(attr, undefined, to, "reversible"));
      out.push(resourcePlan(r.id, "create", changes));
      continue;
    }
    const changes: Change[] = [];
    for (const attr of new Set([...Object.keys(prev.attrs), ...Object.keys(r.attrs)])) {
      // restart_on is apply order, not a guest value: no change of its own.
      if (attr === "restart_on") continue;
      if (!same(prev.attrs[attr], r.attrs[attr])) {
        changes.push(change(attr, prev.attrs[attr], r.attrs[attr], "reversible"));
      }
    }
    if (changes.length) {
      out.push(resourcePlan(r.id, "update", changes));
    }
  }
  // A service restarts after a restart_on resource that changes in this
  // Plan (D-036, D-160); restart_by names these ids (D-171).
  const changed = new Set(out.map((p) => p.resource));
  for (const r of after) {
    const on = (r.attrs["restart_on"] as string[] | undefined) ?? [];
    const by = on.filter((id) => changed.has(id));
    if (r.type === "service" && before.has(r.id) && by.length) {
      out.push({
        ...resourcePlan(r.id, "restart", [{ attr: "restart", risk: "disruptive" }]),
        restart_by: by,
      });
    }
  }
  // The resource that waits shows its `after` ids (D-171).
  const waits = new Map(after.filter((r) => r.after).map((r) => [r.id, r.after]));
  for (const p of out) {
    const a = waits.get(p.resource);
    if (a) p.after = [...a];
  }
  // A resource that is no longer desired is no longer managed: it is not
  // deleted on the guest (D-065), so it gets no plan entry.
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
  } else if (opts.token) {
    session = {
      principal: "token:tok_claude",
      must_change_password: false,
      tier: "plan",
      expires_at: "2026-11-11T08:00:00Z",
    };
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

  // Slice 2 demo data that the examples of the list operations do not show
  // (D-140): the Block "app" with a secret parameter, as in the
  // exportStateWithSecrets example (`db_password: {secret: app-db}`), and
  // the ignore actions on open Issues (served from T-032).
  const appBlock: BlockDoc = {
    kind: "Block",
    name: "app",
    spec: {
      title: "App database config",
      description: "Writes the database settings of the app. The password is a secret.",
      params: {
        type: "object",
        required: ["db_password"],
        properties: {
          db_host: { type: "string", title: "Database host", default: "db1" },
          db_password: { type: "string", title: "Database password", writeOnly: true },
        },
      },
      resources: [
        {
          file: {
            path: "/etc/app/db.conf",
            content: "host={{ .db_host }}\npassword={{ .db_password }}\n",
            mode: "0600",
          },
        },
      ],
    },
  } as BlockDoc;
  blockViews.push({ revision: 7, document: appBlock });
  blocks.set(appBlock.name, appBlock);

  // T-049: the nginx Block (blocks/nginx.yaml, short form) and nginx-site
  // with `after` and `restarts` (D-167, D-172).
  const nginxBlock: BlockDoc = {
    kind: "Block",
    name: "nginx",
    spec: {
      title: "Nginx web server",
      description: "Install Nginx and overwrite the distro default site.",
      params: {
        type: "object",
        properties: { server_name: { type: "string", title: "Server name", default: "_" } },
      },
      resources: [
        { package: { name: "nginx" } },
        {
          file: {
            path: "/etc/nginx/sites-available/default",
            content:
              "server {\n    listen 80 default_server;\n    server_name {{ .server_name }};\n}\n",
            mode: "0644",
          },
        },
        {
          symlink: {
            path: "/etc/nginx/sites-enabled/default",
            target: "/etc/nginx/sites-available/default",
          },
        },
        {
          service: {
            name: "nginx",
            state: "running",
            enabled: true,
            restart_on: [
              "file:/etc/nginx/sites-available/default",
              "symlink:/etc/nginx/sites-enabled/default",
            ],
          },
        },
      ],
    },
  } as BlockDoc;
  const nginxSiteBlock: BlockDoc = {
    kind: "Block",
    name: "nginx-site",
    spec: {
      title: "Nginx site",
      description:
        "One more Nginx site. Use it once per site, with its own use id, on a Target that also uses the nginx Block.",
      after: ["package:nginx"],
      restarts: ["service:nginx"],
      params: {
        type: "object",
        properties: {
          name: { type: "string", title: "Site name", default: "site" },
          server_name: { type: "string", title: "Server name", default: "site.lan" },
          root: { type: "string", title: "Web root", default: "/var/www/html" },
        },
      },
      resources: [
        {
          file: {
            path: "/etc/nginx/sites-available/{{ .name }}",
            content:
              "server {\n    listen 80;\n    server_name {{ .server_name }};\n    root {{ .root }};\n}\n",
            mode: "0644",
          },
        },
        {
          symlink: {
            path: "/etc/nginx/sites-enabled/{{ .name }}",
            target: "/etc/nginx/sites-available/{{ .name }}",
          },
        },
      ],
    },
  } as BlockDoc;
  for (const b of [nginxBlock, nginxSiteBlock]) {
    blockViews.push({ revision: 7, document: b });
    blocks.set(b.name, b);
  }

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
  for (const i of issues.values())
    if (i.status === "open")
      i.actions = [...new Set<IssueAction>([...i.actions, "ignore_once", "ignore_rule"])];
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
  // Revision history (T-034): one document snapshot per revision. The head
  // is the example state; revision 6 is the head with the diffRevision
  // example undone; revision 5 is a seed with the old memory of db1 (the
  // reason of revision 6). An older base is the empty state (from: null).
  type Snapshot = Map<string, StateDocument>;
  const docKey = (d: { kind: string; name: string }) => `${d.kind}/${d.name}`;
  const headDocs: Snapshot = new Map();
  for (const d of [
    ...images.map((i) => i.document),
    ...blockViews.map((b) => b.document),
    ...[...targets.values()].map((t) => t.document),
    ...exampleItems<RuleView>(example("listRules", 200)).map((r) => r.document),
  ] as StateDocument[])
    headDocs.set(docKey(d), d);
  const revisions = new Map<number, { info: RevisionInfo; docs: Snapshot }>();
  {
    const listed = exampleItems<RevisionInfo>(example("listRevisions", 200));
    const diff = example("diffRevision", 200);
    const r6 = new Map(headDocs);
    for (const c of diff.changes) {
      if (c.before) r6.set(docKey(c), c.before);
      else r6.delete(docKey(c));
    }
    const r5 = new Map(r6);
    const db1 = r5.get("Target/db1") as TargetDoc | undefined;
    if (db1) {
      const old = structuredClone(db1);
      (old.spec.guest as Record<string, unknown>)["memory"] = 4096;
      r5.set("Target/db1", old);
    }
    const snaps: Record<number, Snapshot> = { [head]: headDocs, [diff.from ?? 0]: r6, 5: r5 };
    for (const info of listed) {
      const docs = snaps[info.id];
      if (docs) revisions.set(info.id, { info: { ...info, documents: docs.size }, docs });
    }
    revisions.set(5, {
      info: {
        id: 5,
        author: "system",
        reason: "Seeded by the demo.",
        plan_id: null,
        created_at: "2026-11-09T08:00:00Z",
        documents: r5.size,
      },
      docs: r5,
    });
    // The examples of the other screens may name a newer head.
    for (let n = Math.max(...revisions.keys()) + 1; n <= head; n++) {
      revisions.set(n, {
        info: {
          id: n,
          author: "user:admin",
          reason: "Demo revision.",
          plan_id: null,
          created_at: "2026-11-10T09:00:00Z",
          documents: headDocs.size,
        },
        docs: new Map(headDocs),
      });
    }
  }

  function findRevision(n: number) {
    const r = revisions.get(n);
    if (!r) throw problem("not-found", "Not found", 404, `Revision ${n} does not exist.`);
    return r;
  }

  /** Documents that differ from `a` to `b`, sorted by kind and name. */
  function diffDocs(a: Snapshot, b: Snapshot): DocumentChange[] {
    const keys = [...new Set([...a.keys(), ...b.keys()])].sort();
    const out: DocumentChange[] = [];
    for (const k of keys) {
      const before = a.get(k) ?? null;
      const after = b.get(k) ?? null;
      if (same(before, after)) continue;
      const ref = (after ?? before) as StateDocument;
      out.push({
        kind: ref.kind,
        name: ref.name,
        op: !before ? "added" : !after ? "removed" : "changed",
        before: structuredClone(before),
        after: structuredClone(after),
      });
    }
    return out;
  }

  // Tokens and secrets (T-035). The mock keeps secret values only to show
  // conflicts on import; the API never returns them.
  const tokens = new Map(exampleItems<Token>(example("listTokens", 200)).map((t) => [t.id, t]));
  const secrets = new Map(
    exampleItems<SecretInfo>(example("listSecrets", 200)).map((x) => [x.name, x]),
  );
  const secretValues = new Map([...secrets.keys()].map((n) => [n, `mock value of ${n}`]));
  let secretVersion = Math.max(0, ...[...secrets.values()].map((x) => x.version));

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

  /** Tokens, secrets and import need a human session (D-087, D-095). */
  function human(): Session {
    const s = auth();
    if (s.tier !== "admin") {
      throw problem(
        "human-required",
        "Human session required",
        403,
        "Only a person who logged in can manage tokens, secrets, export with secrets and import.",
      );
    }
    return s;
  }

  // Rules and secrets in plans (T-036, D-089, D-092, D-099).
  function headRules(): RuleDoc[] {
    return [...findRevision(head).docs.values()].filter((d) => d.kind === "Rule") as RuleDoc[];
  }
  const ruleMatches = (r: RuleDoc, target: string, resource: string, attr?: string) =>
    (r.spec.target === "*" || r.spec.target === target) &&
    r.spec.resource === resource &&
    (attr === undefined || !r.spec.attrs || r.spec.attrs.includes(attr));

  /** Marks the changes that a rule of the head ignores; the apply still writes them. */
  function markIgnored(tp: { target: string; resources: ResourcePlan[] }) {
    const rules = headRules();
    for (const r of tp.resources)
      for (const c of r.changes)
        if (rules.some((rule) => ruleMatches(rule, tp.target, r.resource, c.attr)))
          c.ignored = true;
  }

  /** Pairs "resource secret" that a target spec binds. */
  function bindings(spec: TargetDoc["spec"] | undefined): Set<string> {
    const out = new Set<string>();
    if (!spec) return out;
    for (const r of render(spec, blocks))
      for (const n of secretsIn(r.attrs)) out.add(`${r.id} ${n}`);
    return out;
  }

  /** TargetPlan.secrets of a spec; a missing secret is 422 (D-089). */
  function plannedSecrets(spec: TargetDoc["spec"]) {
    const names = [...new Set([...bindings(spec)].map((b) => b.split(" ")[1] ?? ""))].sort();
    const missing = names.filter((n) => !secrets.has(n));
    if (missing.length) {
      throw problem("validation", "Validation failed", 422, "Secrets do not exist.", {
        secrets: missing,
      });
    }
    return names.map((name) => ({ name, version: secrets.get(name)?.version ?? 1 }));
  }

  /** The revision that last changed a document of the head. */
  function lastChanged(key: string): number {
    let at = head;
    const now = JSON.stringify(findRevision(head).docs.get(key));
    for (const id of [...revisions.keys()].sort((a, b) => b - a)) {
      if (JSON.stringify(revisions.get(id)?.docs.get(key)) !== now) break;
      at = id;
    }
    return at;
  }

  /**
   * After a commit: open Issues (and the one the Plan resolves) that a rule
   * fully covers resolve as `ignore`; targets record the dropped drift.
   */
  function applyRules(by: string, planId: number) {
    const rules = headRules();
    for (const t of targets.values()) {
      const kept = (t.status.ignored_drift ?? []).filter((d) =>
        rules.some((r) => r.name === d.rule),
      );
      if (kept.length) t.status.ignored_drift = kept;
      else delete t.status.ignored_drift;
    }
    for (const issue of issues.values()) {
      const mine = issue.status === "resolving" && issue.plan_id === planId;
      if (issue.status !== "open" && !mine) continue;
      const rule = rules.find((r) =>
        issue.changes.every((c) => ruleMatches(r, issue.target, issue.resource, c.attr)),
      );
      if (!rule) continue;
      issue.status = "resolved";
      issue.actions = [];
      issue.resolution = { action: "ignore", by, at: now() };
      const target = targets.get(issue.target);
      if (target) {
        target.status.open_issues = Math.max(0, target.status.open_issues - 1);
        if (target.status.open_issues === 0 && target.status.phase === "drifted")
          target.status.phase = "ready";
        const list = (target.status.ignored_drift ?? []).filter(
          (d) => d.resource !== issue.resource,
        );
        list.push({
          resource: issue.resource,
          rule: rule.name,
          attrs: issue.changes.map((c) => c.attr),
          last_seen: issue.updated_at,
        });
        target.status.ignored_drift = list;
      }
      emit("issue.resolved", `issue:${issue.id}`, { plan_id: planId });
    }
  }

  function findPlan(id: number): Plan {
    const p = plans.get(id);
    if (!p) throw problem("not-found", "Not found", 404, `Plan ${id} does not exist.`);
    return p;
  }

  function newPlan(
    fields: Pick<Plan, "origin" | "issue_id" | "reason" | "targets" | "warnings"> &
      Partial<Pick<Plan, "human_only" | "rollback_to">>,
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

  /**
   * Import in the mock: it checks the header, the secrets and the bundle like
   * the server (D-095), but it does not parse the documents. The Plan is the
   * importState example with the reason and the secret warnings.
   */
  function importMock(req: ImportRequest): Plan {
    const invalid = (pointer: string, message: string, extra?: Partial<Problem>) =>
      problem("validation", "Validation failed", 422, undefined, {
        errors: [{ pointer, message }],
        ...extra,
      });
    if (!req.reason.trim()) throw invalid("/reason", "A reason is required.");
    if (!/^kind:\s*Export\s*$/m.test(req.content) || !/^format:\s*1\s*$/m.test(req.content))
      throw invalid("/content", "This is not a Plumb export file of format 1.");
    let bundle: Record<string, string> | undefined;
    const data = /kind:\s*SecretBundle[\s\S]*?\bdata:\s*"?([A-Za-z0-9+/=]+)"?/.exec(
      req.content,
    )?.[1];
    if (req.passphrase && data) {
      let parsed: { check?: string; values?: Record<string, string> } = {};
      try {
        parsed = JSON.parse(fromBase64(data)) as typeof parsed;
      } catch {
        // falls through to the passphrase error
      }
      if (parsed.check !== mockHash(req.passphrase) || !parsed.values)
        throw invalid("/passphrase", "The passphrase does not open the secret bundle.");
      bundle = parsed.values;
    }
    const overwrite = req.overwrite ?? [];
    const notInBundle = overwrite.filter((n) => !bundle || !(n in bundle));
    if (notInBundle.length)
      throw invalid("/overwrite", `Not in the secret bundle: ${notInBundle.join(", ")}.`);
    const conflicts = Object.entries(bundle ?? {})
      .filter(
        ([n, v]) => secretValues.has(n) && secretValues.get(n) !== v && !overwrite.includes(n),
      )
      .map(([n]) => n)
      .sort();
    if (conflicts.length) {
      const e = example("importState", 409);
      throw new ApiError({
        ...e,
        detail: `${conflicts.length} secret${conflicts.length === 1 ? "" : "s"} of the file differ${conflicts.length === 1 ? "s" : ""} from the stored value. Nothing was written.`,
        secrets: conflicts,
      });
    }
    const referenced = new Set(
      [...req.content.matchAll(/\bsecret:\s*"?([a-z0-9][a-z0-9.-]*)"?/g)].map((m) => m[1] ?? ""),
    );
    const missing = [...referenced]
      .filter((n) => n && !secretValues.has(n) && !(bundle && n in bundle))
      .sort();
    if (missing.length) {
      throw problem(
        "validation",
        "Validation failed",
        422,
        "The file refers to secrets that do not exist.",
        { secrets: missing },
      );
    }
    const warnings: string[] = [];
    const s = human();
    for (const [n, v] of Object.entries(bundle ?? {})) {
      const had = secretValues.get(n);
      if (had === v) continue;
      secretValues.set(n, v);
      secrets.set(n, {
        name: n,
        version: ++secretVersion,
        updated_by: s.principal,
        updated_at: now(),
        used_by: secrets.get(n)?.used_by ?? [],
      });
      warnings.push(
        had === undefined
          ? `Secret ${n} was created from the file. It stays when you drop this plan.`
          : `Secret ${n} was overwritten from the file. It stays when you drop this plan.`,
      );
    }
    const ex = example("importState", 201);
    const p = newPlan({
      origin: "import",
      issue_id: null,
      reason: req.reason,
      targets: ex.targets,
      warnings: [...ex.warnings, ...warnings],
    });
    return p;
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
      const planTargets: Plan["targets"] = [];
      const humanOnly = new Set<"secret-binding" | "rule-change">();
      const warnings: string[] = [];
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
        const old = targets.get(t.name)?.document.spec;
        let resources = planTarget(old, t.spec, blocks, targets.get(t.name)?.status.power);
        const used = plannedSecrets(t.spec);
        const before = bindings(old);
        const fresh = [...bindings(t.spec)].filter((b) => !before.has(b));
        if (fresh.length) humanOnly.add("secret-binding");
        // A resource of an existing target with a new binding is not observed
        // before the approval: create, unverified, disruptive (D-156).
        if (old && fresh.length) {
          const ids = new Set(fresh.map((b) => b.split(" ")[0] ?? ""));
          const unverified = render(t.spec, blocks)
            .filter((r) => ids.has(r.id))
            .map((r) => ({
              resource: r.id,
              action: "create" as const,
              risk: "disruptive" as Risk,
              unverified: true,
              changes: Object.entries(r.attrs)
                .filter(([, v]) => hasMarker(v))
                .map(([attr]) => ({ attr, sensitive: true as const, risk: "disruptive" as Risk })),
            }));
          resources = [...resources.filter((r) => !ids.has(r.resource)), ...unverified];
          warnings.push(
            `${t.name}: ${ids.size} resources are not observed before the approval: they get a new secret, or a value that the import writes (D-156, D-158).`,
          );
        }
        planTargets.push({ target: t.name, ...(used.length ? { secrets: used } : {}), resources });
      }
      // Targets planned without a document change (a new secret value, D-090).
      for (const name of proposal.targets ?? []) {
        if (planTargets.some((t) => t.target === name)) continue;
        const t = targets.get(name);
        if (!t) {
          throw problem("validation", "Validation failed", 422, `Target "${name}" does not exist.`);
        }
        const used = plannedSecrets(t.document.spec);
        const rendered = render(t.document.spec, blocks).filter((r) => secretsIn(r.attrs).length);
        // The examples bind no secret to a target; the mock then shows one hidden change.
        const resources = rendered.length
          ? rendered.map((r) =>
              resourcePlan(
                r.id,
                "update",
                Object.entries(r.attrs)
                  .filter(([, v]) => hasMarker(v))
                  .map(([attr]) => ({
                    attr,
                    sensitive: true as const,
                    risk: "reversible" as Risk,
                  })),
              ),
            )
          : [
              resourcePlan("file:/etc/plumb/secrets", "update", [
                { attr: "content", sensitive: true, risk: "reversible" },
              ]),
            ];
        planTargets.push({ target: name, ...(used.length ? { secrets: used } : {}), resources });
      }
      for (const d of [...(proposal.put ?? []), ...(proposal.delete ?? [])])
        if (d.kind === "Rule") humanOnly.add("rule-change");
      planTargets.forEach(markIgnored);
      const p = newPlan({
        origin: "proposal",
        issue_id: null,
        reason: proposal.reason,
        targets: planTargets,
        warnings,
        human_only: [...humanOnly],
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
      // Only a person approves a human-only Plan (D-099; QA T-036 F2).
      if (p.status === "pending" && (p.human_only?.length ?? 0) > 0 && s.tier !== "admin") {
        throw problem(
          "human-required",
          "Human session required",
          403,
          "Only a person can approve this plan.",
        );
      }
      if (p.status === "stale" || (p.status === "pending" && p.base_revision !== head)) {
        if (p.status !== "stale") {
          p.status = "stale";
          emit("plan.stale", `plan:${p.id}`, {});
        }
        throw new ApiError(example("approvePlan", 409));
      }
      if (p.status !== "pending")
        throw problem("conflict", "Plan is not pending", 409, `Plan ${id} is ${p.status}.`);
      const proposal = pendingDocs.get(p.id);
      const docs = new Map(findRevision(head).docs);
      for (const d of proposal?.put ?? []) docs.set(docKey(d), structuredClone(d));
      for (const d of proposal?.delete ?? []) docs.delete(docKey(d));
      head += 1;
      revisions.set(head, {
        info: {
          id: head,
          author: s.principal,
          reason: p.reason,
          plan_id: p.id,
          created_at: now(),
          documents: docs.size,
        },
        docs,
      });
      p.status = "approved";
      p.approved_by = s.principal;
      p.approved_at = now();
      p.revision = head;
      const run: Run = {
        id: nextRun++,
        plan_id: p.id,
        revision: head,
        status: "running",
        waits_for_restart: 0,
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
      emit("revision.committed", `revision:${head}`, { plan_id: p.id });
      const job = simulate(run, p, () => {
        for (const ref of proposal?.delete ?? [])
          if (ref.kind === "Target") targets.delete(ref.name);
        for (const doc of proposal?.put ?? []) {
          if (doc.kind === "Block") {
            const b = doc as BlockDoc;
            const view = blockViews.find((v) => v.document.name === b.name);
            if (view) Object.assign(view, { revision: head, document: b });
            else blockViews.push({ revision: head, document: b });
            blocks.set(b.name, b);
          }
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
              waiting: [],
            },
          };
          view.status.last_run_id = run.id;
          view.status.applied_revision = head;
          targets.set(t.name, view);
        }
        // An ignore_rule Plan resolves its issue in applyRules below.
        const issue = p.issue_id && p.origin !== "ignore_rule" ? issues.get(p.issue_id) : undefined;
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
        applyRules(s.principal, p.id);
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
    async listIssues(filter) {
      auth();
      const list = [...issues.values()].filter(
        (i) =>
          (!filter?.target || i.target === filter.target) &&
          (!filter?.status || i.status === filter.status),
      );
      return structuredClone(list.sort((a, b) => b.id - a.id));
    },
    async getIssue(id) {
      auth();
      const i = issues.get(id);
      if (!i) throw new ApiError(example("getIssue", 404));
      return structuredClone(i);
    },
    async resolveIssue(id, action, reason, rule) {
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
      let p: Plan;
      if (action === "ignore_rule") {
        // One Rule through a Plan (D-092); only a person approves it (D-099).
        const all = rule?.all_targets === true;
        const slug = issue.resource.replace(/[^a-z0-9]+/gi, "-").toLowerCase();
        const base = `ignore-${all ? "all" : issue.target}-${slug}`.slice(0, 55).replace(/-+$/, "");
        const taken = new Set(headRules().map((r) => r.name));
        let name = base;
        for (let n = 2; taken.has(name); n++) name = `${base}-${n}`;
        const doc: RuleDoc = {
          kind: "Rule",
          name,
          spec: {
            action: "ignore",
            target: all ? "*" : issue.target,
            resource: issue.resource,
            ...(rule?.attrs?.length ? { attrs: rule.attrs } : {}),
          },
        };
        p = newPlan({
          origin: "ignore_rule",
          issue_id: id,
          reason: reason?.trim() || `Ignore drift of ${issue.resource}.`,
          targets: [],
          warnings: [],
          human_only: ["rule-change"],
        });
        pendingDocs.set(p.id, { reason: p.reason, put: [doc as StateDocument] });
      } else {
        const changes: Change[] =
          action === "revert"
            ? structuredClone(issue.changes)
            : issue.changes.map((c) =>
                "sensitive" in c
                  ? c
                  : { attr: c.attr, from: c.to, to: c.from, risk: "read" as Risk },
              );
        const tp = {
          target: issue.target,
          resources: [resourcePlan(issue.resource, "update", changes)],
        };
        markIgnored(tp);
        p = newPlan({
          origin: action,
          issue_id: id,
          reason:
            reason?.trim() || (action === "revert" ? "Revert drift." : "Accept the guest value."),
          targets: [tp],
          warnings:
            action === "accept"
              ? [
                  "Accept writes the guest value into the Target as an override. The guest does not change.",
                ]
              : [],
        });
      }
      issue.status = "resolving";
      issue.plan_id = p.id;
      issue.actions = [];
      emit("issue.updated", `issue:${id}`, { plan_id: p.id });
      return p;
    },
    async ignoreOnce(id) {
      const s = auth();
      const issue = issues.get(id);
      if (!issue) throw new ApiError(example("getIssue", 404));
      // A person, or an apply token within its max risk and without sensitive changes (D-091).
      if (s.tier !== "admin") {
        const order: Risk[] = ["read", "reversible", "disruptive", "destructive"];
        const within =
          s.tier === "apply" &&
          !issue.changes.some((c) => "sensitive" in c) &&
          order.indexOf(issue.risk) <= order.indexOf(s.max_risk ?? "read");
        if (!within) {
          throw s.tier === "apply"
            ? problem(
                "risk-too-high",
                "Risk exceeds token limit",
                403,
                "This token cannot revert this drift, so it cannot ignore it.",
              )
            : problem(
                "human-required",
                "Human session required",
                403,
                "This token cannot revert this drift, so it cannot ignore it. Ask a person.",
              );
        }
      }
      if (issue.status !== "open" || !issue.actions.includes("ignore_once")) {
        throw problem("conflict", "Action not allowed", 409, `Issue ${id} is not open.`);
      }
      issue.status = "resolved";
      issue.actions = [];
      issue.updated_at = now();
      issue.resolution = { action: "ignore", by: s.principal, at: now() };
      const target = targets.get(issue.target);
      if (target) {
        target.status.open_issues = Math.max(0, target.status.open_issues - 1);
        if (target.status.open_issues === 0 && target.status.phase === "drifted")
          target.status.phase = "ready";
      }
      emit("issue.resolved", `issue:${id}`, {});
      return structuredClone(issue);
    },
    async listRules() {
      auth();
      return headRules()
        .sort((a, b) => a.name.localeCompare(b.name))
        .map((document) => structuredClone({ revision: lastChanged(docKey(document)), document }));
    },
    async listRevisions(cursor, limit = 50) {
      auth();
      const all = [...revisions.values()].map((r) => r.info).sort((a, b) => b.id - a.id);
      // The mock cursor is the id of the first item of the page.
      const from = cursor ? Number(cursor) : Infinity;
      const rest = all.filter((r) => r.id <= from);
      const items = rest.slice(0, limit);
      const next = rest[limit];
      return structuredClone({ items, next: next ? String(next.id) : null });
    },
    async diffRevision(n, from) {
      auth();
      const to = findRevision(n);
      const base = from ?? n - 1;
      if (from !== undefined) findRevision(from);
      const before = revisions.get(base);
      return {
        from: before ? base : null,
        to: n,
        changes: diffDocs(before?.docs ?? new Map(), to.docs),
      };
    },
    async reenrollTarget(name, reason) {
      auth();
      const t = targets.get(name);
      if (!t) throw problem("not-found", "Not found", 404, `Target ${name} does not exist.`);
      // D-194: a running VM with allow_restart off is not restarted.
      if (t.document.spec.allow_restart !== true && t.status.power === "running") {
        throw problem(
          "validation",
          "Validation failed",
          422,
          `plan: target ${name}: ${REENROLL_REFUSED}`,
        );
      }
      return newPlan({
        origin: "reenroll",
        issue_id: null,
        reason: reason || `re-enroll ${name}`,
        targets: [
          {
            target: name,
            resources: [
              resourcePlan("guest:config", "update", [
                { attr: "enroll_token", sensitive: true, risk: "disruptive" },
              ]),
            ],
          },
        ],
        warnings: [],
      });
    },
    async rollbackRevision(n, reason) {
      auth();
      if (!reason.trim()) {
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [{ pointer: "/reason", message: "A reason is required." }],
        });
      }
      const target = findRevision(n);
      const changes = diffDocs(findRevision(head).docs, target.docs);
      if (changes.length === 0) {
        const e = example("rollbackRevision", 409);
        throw new ApiError({
          ...e,
          detail: `Revision ${n} has the same documents as the head revision.`,
        });
      }
      const proposal: Proposal = {
        reason,
        put: changes.flatMap((c) => (c.after ? [c.after] : [])),
        delete: changes.filter((c) => !c.after).map((c) => ({ kind: c.kind, name: c.name })),
      };
      const planTargets = changes
        .filter((c) => c.kind === "Target" && c.after)
        .map((c) => ({
          target: c.name,
          resources: planTarget(
            targets.get(c.name)?.document.spec,
            (c.after as TargetDoc).spec,
            blocks,
            targets.get(c.name)?.status.power,
          ),
        }));
      const warnings = changes
        .filter((c) => c.kind === "Target" && !c.after)
        .map((c) => `Target ${c.name} is removed from Plumb. Plumb does not delete its guest.`);
      const p = newPlan({
        origin: "rollback",
        rollback_to: n,
        issue_id: null,
        reason,
        targets: planTargets,
        warnings,
      });
      pendingDocs.set(p.id, proposal);
      return p;
    },
    async listTokens() {
      human();
      return structuredClone(
        [...tokens.values()].sort((a, b) => b.created_at.localeCompare(a.created_at)),
      );
    },
    async createToken(req) {
      const s = human();
      const errors: { pointer: string; message: string }[] = [];
      if (!req.name.trim() || req.name.length > 100)
        errors.push({ pointer: "/name", message: "Use 1 to 100 characters." });
      if (req.tier === "apply" && !req.max_risk)
        errors.push({ pointer: "/max_risk", message: "An apply token needs a max risk." });
      if (req.tier !== "apply" && req.max_risk)
        errors.push({ pointer: "/max_risk", message: "Only an apply token has a max risk." });
      if (errors.length)
        throw problem("validation", "Validation failed", 422, undefined, { errors });
      const base = req.name
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "_")
        .replace(/^_|_$/g, "");
      let id = `tok_${base || "token"}`;
      for (let i = 2; tokens.has(id); i++) id = `tok_${base}_${i}`;
      const token: Token = {
        id,
        name: req.name.trim(),
        tier: req.tier,
        max_risk: req.tier === "apply" ? (req.max_risk ?? null) : null,
        created_by: s.principal,
        created_at: now(),
        last_used_at: null,
        revoked_at: null,
      };
      tokens.set(id, token);
      emit("token.created", `token:${id}`, {});
      const rnd = crypto.getRandomValues(new Uint8Array(16));
      const bearer = `${id}.${[...rnd].map((b) => b.toString(16).padStart(2, "0")).join("")}`;
      const out: TokenCreated = { token: structuredClone(token), bearer };
      return out;
    },
    async revokeToken(id) {
      human();
      const t = tokens.get(id);
      if (!t) throw problem("not-found", "Not found", 404, `Token ${id} does not exist.`);
      if (!t.revoked_at) {
        t.revoked_at = now();
        emit("token.revoked", `token:${id}`, {});
      }
    },
    async listSecrets() {
      auth();
      return structuredClone([...secrets.values()].sort((a, b) => a.name.localeCompare(b.name)));
    },
    async putSecret(name, value) {
      const s = human();
      if (!/^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$/.test(name)) {
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [
            {
              pointer: "/name",
              message: "Use lower-case letters, digits, dots and dashes (at most 63).",
            },
          ],
        });
      }
      if (value.length === 0 || new TextEncoder().encode(value).length > 65536) {
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [{ pointer: "/value", message: "Use 1 to 65536 bytes." }],
        });
      }
      const info: SecretInfo = {
        name,
        version: ++secretVersion,
        updated_by: s.principal,
        updated_at: now(),
        used_by: secrets.get(name)?.used_by ?? [],
      };
      secrets.set(name, info);
      secretValues.set(name, value);
      emit("secret.updated", `secret:${name}`, { name });
      return structuredClone(info);
    },
    async deleteSecret(name) {
      human();
      const info = secrets.get(name);
      if (!info) throw problem("not-found", "Not found", 404, `Secret "${name}" does not exist.`);
      if (info.used_by.length) {
        throw problem(
          "secret-in-use",
          "Secret is in use",
          409,
          `Secret "${name}" is used by ${info.used_by.length} target${info.used_by.length === 1 ? "" : "s"}.`,
          { targets: [...info.used_by], plans: [] },
        );
      }
      secrets.delete(name);
      secretValues.delete(name);
      emit("secret.deleted", `secret:${name}`, { name });
    },
    async exportState(passphrase) {
      auth();
      if (passphrase !== undefined) {
        human();
        if (passphrase.length < 12) {
          throw problem("validation", "Validation failed", 422, undefined, {
            errors: [{ pointer: "/passphrase", message: "Use at least 12 characters." }],
          });
        }
      }
      const docs = [...findRevision(head).docs.values()].sort((a, b) =>
        a.kind === b.kind ? a.name.localeCompare(b.name) : a.kind.localeCompare(b.kind),
      );
      const header = {
        kind: "Export",
        format: 1,
        revision: head,
        exported_at: now(),
        version: "mock",
        secrets: [...secrets.keys()].sort(),
      };
      const parts: unknown[] = [header, ...docs];
      if (passphrase !== undefined) {
        // Not encryption: the mock only needs to read its own bundle again.
        const values = Object.fromEntries(secretValues);
        parts.push({
          kind: "SecretBundle",
          kdf: { name: "mock" },
          salt: "",
          nonce: "",
          data: toBase64(JSON.stringify({ check: mockHash(passphrase), values })),
        });
      }
      return parts.map((p) => toYaml(p)).join("---\n");
    },
    async importState(req: ImportRequest) {
      human();
      return importMock(req);
    },
    events(since, onEvent, onOpen) {
      if (since !== undefined) for (const e of log) if (e.seq > since) onEvent(e);
      listeners.add(onEvent);
      if (onOpen) queueMicrotask(onOpen);
      return () => listeners.delete(onEvent);
    },
    async idle() {
      while (active.size) await Promise.all([...active]);
    },
  };
  const pendingDocs = new Map<number, Proposal>();

  // A demo Plan with nginx and two sites (T-049 screenshots, "/#/plans/<id>"):
  // web1 as if it had nginx and site a, then site a changes and site b is new.
  // It shows "after" on the site files and the cause of the nginx restart.
  const web1 = targets.get("web1")?.document;
  if (web1) {
    const site = (name: string, server: string) => ({
      use: "nginx-site",
      id: `site-${name}`,
      params: { name, server_name: server },
    });
    const base = structuredClone(web1.spec);
    base.blocks = [...(base.blocks ?? []), { use: "nginx" }, site("a", "a.lan")];
    const next = structuredClone(base);
    next.blocks = [
      ...(next.blocks ?? []).slice(0, -1),
      site("a", "a.lan www.a.lan"),
      site("b", "b.lan"),
    ];
    const resources = planTarget(base, next, blocks);
    const p: Plan = {
      id: nextPlan++,
      status: "pending",
      origin: "proposal",
      issue_id: null,
      reason: "Demo: two Nginx sites on web1",
      risk: maxRisk(resources.flatMap((r) => r.changes.map((c) => c.risk))),
      author: "user:admin",
      created_at: "2026-11-10T09:30:00Z",
      base_revision: head,
      approved_by: null,
      approved_at: null,
      revision: null,
      run_id: null,
      warnings: [],
      targets: [{ target: "web1", resources }],
    };
    plans.set(p.id, p);
    api.orderPlanId = p.id;
    pendingDocs.set(p.id, { reason: p.reason, put: [{ ...web1, spec: next }] });
  }
  return api;
}

/** Base64 of UTF-8 text (mock bundle only). */
function toBase64(text: string): string {
  let bin = "";
  for (const b of new TextEncoder().encode(text)) bin += String.fromCharCode(b);
  return btoa(bin);
}

function fromBase64(b64: string): string {
  return new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)));
}

/** A tag for the passphrase of a mock bundle. Not a security measure. */
function mockHash(s: string): string {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return (h >>> 0).toString(16);
}

// In-memory mock of the Plumb API for T-006. Its data comes from the
// examples of api/openapi.yaml; it computes plans, runs and events the way
// the server does (ARCHITECTURE §3, §4), in a much simpler form.
import { ApiError } from "../api";
import type {
  CatalogBlock,
  PinRefresh,
  PlumbApi,
  RepairKeyView,
  RepairState,
  Settings,
  UnmanagedGuest,
  User,
  UserRole,
} from "../client";
import roles from "../roles.gen.json";
import type {
  BlockDoc,
  BlockView,
  DocumentChange,
  DiskUse,
  ImageDoc,
  ImagePlan,
  ImageRun,
  ImageSpec,
  ImageVersion,
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
  BackupList,
  BackupRun,
  RestoreState,
  TokenCreated,
  TargetDoc,
  TargetView,
} from "../types";
import { CREATE_ONLY, maxRisk, REENROLL_REFUSED } from "../view";
import { diskUse, nextSlot } from "../images";
import { toYaml } from "../yaml";
import { checkBlock } from "../blockCheck";
import { usersOf } from "../blocks";
import { secretName } from "../schemaForm";
import { example, exampleItems } from "./examples";
import { buildRuns, diskRefusal, GIB, seedImages, seedLxc, seedTargets } from "./imageSeed";

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
  /** Start as a restored controller in restored mode (D-290, D-303). */
  restored?: boolean;
  /**
   * Start signed in as a person with this role (D-329): admin → admin,
   * apply → alice, plan → carol, read → bob (T-154).
   */
  role?: UserRole;
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

/** The Block use that renders a resource, or null (D-301: Issue.source, RawResource.source). */
export function sourceOf(
  spec: TargetDoc["spec"],
  blocks: Map<string, BlockDoc>,
  resource: string,
): { block: string; use: string } | null {
  if (resource.startsWith("guest:") || resource.startsWith("agent:")) return null;
  for (const u of spec.blocks ?? []) {
    try {
      const ids = render({ ...spec, blocks: [u], overrides: {} }, blocks).map((r) => r.id);
      if (ids.includes(resource)) return { block: u.use, use: u.id ?? u.use };
    } catch {
      // A use alone can fail its `after` check; it is then not the source.
    }
  }
  return null;
}

/**
 * D-362 in the mock: Accept maps the drift back to Block parameters when
 * every changed attribute's template is exactly `<prefix>{{ .p }}<suffix>`
 * and the observed value (`from`) has that prefix and suffix. Else raw.
 */
export function acceptMapping(
  spec: TargetDoc["spec"],
  blocks: Map<string, BlockDoc>,
  issue: Pick<Issue, "resource" | "changes" | "actions">,
): Pick<Issue, "accept_mode" | "accept_reason" | "accept_params"> {
  if (!issue.actions.includes("accept"))
    return { accept_mode: "none", accept_reason: "", accept_params: [] };
  const raw = (why: string) => ({
    accept_mode: "raw" as const,
    accept_reason: why,
    accept_params: [],
  });
  const uses = spec.blocks ?? [];
  for (const [index, u] of uses.entries()) {
    let ids: string[];
    try {
      ids = render({ ...spec, blocks: [u], overrides: {} }, blocks).map((r) => r.id);
    } catch {
      continue;
    }
    const at = ids.indexOf(issue.resource);
    const entry = blocks.get(u.use)?.spec.resources?.[at];
    if (at < 0 || !entry) continue;
    const attrs = Object.values(entry)[0] as Record<string, unknown>;
    const out: { use: string; index: number; param: string }[] = [];
    for (const c of issue.changes) {
      if ("sensitive" in c) return raw("a secret value cannot go into a parameter");
      const t = attrs[c.attr];
      const m = typeof t === "string" ? /^([^{}]*)\{\{\s*\.(\w+)\s*\}\}([^{}]*)$/s.exec(t) : null;
      if (!m) return raw(`the template of ${c.attr} is not one plain parameter`);
      const [, pre = "", param = "", suf = ""] = m;
      const seen = String(c.from ?? "");
      if (!seen.startsWith(pre) || !seen.endsWith(suf))
        return raw(`the guest value of ${c.attr} does not fit the template`);
      if (!out.some((o) => o.param === param)) out.push({ use: u.use, index, param });
    }
    return { accept_mode: "param", accept_reason: "", accept_params: out };
  }
  return raw("no Block renders this resource");
}

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
  /** The Blocks of the base revision, when the proposal changes Blocks (T-096). */
  oldBlocks: Map<string, BlockDoc> = blocks,
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
      // D-291, D-299: the disk only grows (a grow cannot be undone).
      if (attr === "disk" && Number(g[attr]) < Number(og[attr])) {
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [
            {
              pointer: `/put/0/spec/guest/disk`,
              message: `disk can only grow: ${String(og[attr])} GiB now, ${String(g[attr])} GiB asked.`,
            },
          ],
        });
      }
      // A start and a tag change are reversible; a stop and a grow are disruptive.
      const risk: Risk =
        attr === "tags" || (attr === "power" && g[attr] !== "stopped")
          ? "reversible"
          : "disruptive";
      changes.push({ attr, from: og[attr], to: g[attr], risk });
    }
    if (g["power"] === "stopped" && next.allow_restart !== true) {
      throw problem("validation", "Validation failed", 422, undefined, {
        errors: [
          {
            pointer: `/put/0/spec/guest/power`,
            message: "power: stopped needs allow_restart (D-291).",
          },
        ],
      });
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

  const before = new Map((old ? render(old, oldBlocks) : []).map((r) => [r.id, r]));
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

/** The agent this mock controller serves (T-097). */
export const MOCK_AGENT_VERSION = "v0.4.0";

/**
 * Agent versions and update states (D-257 … D-259): web1 can update,
 * cache1 must (its state needs `unit`; its last update went back),
 * backup1 is older than slice 3 (manual), every other agent is current.
 */
function seedAgents(targets: Map<string, TargetView>): void {
  for (const t of targets.values()) {
    const s = t.status;
    const seen = s.agent !== "none";
    Object.assign(s, {
      agent_version: seen ? MOCK_AGENT_VERSION : null,
      agent_update: seen ? "none" : "unknown",
      agent_missing: [],
      agent_reverted: null,
    });
  }
  const set = (name: string, status: Partial<TargetView["status"]>) => {
    const t = targets.get(name);
    if (t) Object.assign(t.status, status);
  };
  set("web1", { agent_version: "v0.3.0", agent_update: "available" });
  set("cache1", {
    agent_version: "v0.3.0",
    agent_update: "required",
    agent_missing: ["unit"],
    agent_reverted: {
      from: "v0.3.0",
      to: MOCK_AGENT_VERSION,
      reason: "no-hello",
      at: "2026-11-12T21:40:00Z",
    },
  });
  set("backup1", { agent_version: "v0.2.0", agent_update: "manual" });
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
  if (opts.role) {
    const name = { admin: "admin", apply: "alice", plan: "carol", read: "bob" }[opts.role];
    session = {
      principal: `user:${name}`,
      must_change_password: false,
      tier: opts.role,
      expires_at: "2026-11-11T08:00:00Z",
    };
    mustChange = false;
  }

  const images = exampleItems<ImageView>(example("listImages", 200));
  seedImages(images);
  // T-154: the stock Ubuntu Image checks its pin against the signed list (D-335).
  const ubuntu = images.find((i) => i.document.name === "ubuntu-26.04");
  if (ubuntu) ubuntu.document.spec.verify = { source: "ubuntu-cloud" };
  const blockViews = exampleItems<BlockView>(example("listBlocks", 200));
  const blocks = new Map(blockViews.map((b) => [b.document.name, b.document]));
  const targets = new Map(
    exampleItems<TargetView>(example("listTargets", 200)).map((t) => [t.document.name, t]),
  );
  seedTargets(targets);
  // T-118 (D-296): worker1 keeps an accepted motd as a raw resource.
  const worker1 = targets.get("worker1");
  if (worker1)
    worker1.document.spec.overrides = {
      "file:/etc/motd": { content: "Maintenance window: Sunday 02:00 UTC\n" },
    };
  seedLxc(images, targets);
  seedAgents(targets);
  // T-194 (D-384): SSH repair readiness per Target. web3's agent is dead
  // while its guest runs: the repair demo. The fingerprints are examples.
  const FP = (c: string) => `SHA256:${c.repeat(43)}`;
  const repairOf: Record<string, RepairState> = {
    web1: {
      state: "ready",
      reasons: [],
      port: 22,
      host_keys: [FP("a")],
      host_keys_seen: [],
      key_confirmed: true,
    },
    db1: {
      state: "ready",
      reasons: [],
      port: 22,
      host_keys: [FP("b")],
      host_keys_seen: [FP("c")],
      key_confirmed: true,
    },
    cache1: {
      state: "off",
      reasons: ["root-refused"],
      port: 22,
      host_keys: [FP("d")],
      host_keys_seen: [],
      key_confirmed: true,
    },
    backup1: {
      state: "off",
      reasons: ["no-line"],
      port: null,
      host_keys: [],
      host_keys_seen: [],
      key_confirmed: false,
    },
    worker1: {
      state: "ready",
      reasons: [],
      port: 2222,
      host_keys: [FP("e")],
      host_keys_seen: [],
      key_confirmed: false,
    },
    web3: {
      state: "ready",
      reasons: [],
      port: 22,
      host_keys: [FP("f")],
      host_keys_seen: [],
      key_confirmed: true,
    },
  };
  for (const t of targets.values())
    t.status.repair =
      repairOf[t.document.name] ??
      (t.document.spec.type === "lxc"
        ? {
            state: "off",
            reasons: ["no-sshd"],
            port: null,
            host_keys: [],
            host_keys_seen: [],
            key_confirmed: false,
          }
        : { state: "unknown", reasons: [] });
  const web3 = targets.get("web3");
  if (web3) Object.assign(web3.status, { agent: "offline", last_seen: "2026-11-10T07:00:00Z" });
  // D-373: guests of the pool that no Target holds (the contract example).
  const unmanaged: UnmanagedGuest[] = exampleItems<UnmanagedGuest>(
    example("listUnmanagedGuests", 200),
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

  // T-118: a VM-only Block (sysctl, D-260); the pickers hide it for a container.
  const tuneBlock: BlockDoc = {
    kind: "Block",
    name: "net-tuning",
    spec: {
      title: "Network tuning",
      description:
        "Turns on IPv4 forwarding. Virtual machines only: a container shares the host kernel.",
      resources: [{ sysctl: { name: "net.ipv4.ip_forward", value: 1 } }],
    },
  } as BlockDoc;
  blockViews.push({ revision: 7, document: tuneBlock });
  blocks.set(tuneBlock.name, tuneBlock);

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
    example("buildImage", 201), // image build contract (T-058); behaviour: T-064
  ]) {
    if (!plans.has(p.id)) plans.set(p.id, p);
  }
  const runs = new Map<number, Run>();
  for (const r of [
    ...exampleItems<Run>(example("listRuns", 200)),
    example("getRun", 200),
    ...buildRuns(),
  ])
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

  // D-301: raw resources of a Target and the raw offer of an Issue.
  const withRaw = (t: TargetView): TargetView => ({
    ...t,
    raw: Object.entries(t.document.spec.overrides ?? {})
      .sort(([a], [b]) => (a < b ? -1 : 1))
      .map(([resource, o]) => ({
        resource,
        attrs: Object.keys(o ?? {}).sort(),
        source: sourceOf(t.document.spec, blocks, resource),
      })),
  });
  const withSource = (i: Issue): Issue => {
    const t = targets.get(i.target);
    const source = t ? sourceOf(t.document.spec, blocks, i.resource) : null;
    const map = t
      ? acceptMapping(t.document.spec, blocks, i)
      : { accept_mode: "none" as const, accept_reason: "", accept_params: [] };
    return {
      ...i,
      source,
      raw: source !== null && map.accept_mode === "raw",
      ...(source !== null ? map : {}),
    };
  };
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
      // D-128: a revision's author is its Plan's author (the examples differ).
      const author = (info.plan_id !== null && plans.get(info.plan_id)?.author) || info.author;
      if (docs) revisions.set(info.id, { info: { ...info, author, documents: docs.size }, docs });
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

  // T-118: backups and the restore state (D-288 … D-290, D-302 … D-304).
  const backups: BackupList = structuredClone(example("listBackups", 200));
  // D-361: the settings. The install values are the ones of the backup list.
  const installBackup = { time: backups.schedule.time, keep: backups.schedule.keep };
  const settings: Settings = structuredClone(example("getSettings", 200));
  // D-382: the repair key; worker1's line still holds the previous one.
  const repairKey: RepairKeyView = {
    fingerprint: "SHA256:Zm9vYmFyYmF6cXV4cXV1eGNvcmdlZ3JhdWx0Z2FycGx5",
    created_at: "2026-11-14T10:00:00Z",
    previous: {
      fingerprint: "SHA256:b2xka2V5b2xka2V5b2xka2V5b2xka2V5b2xka2V5b2w",
      created_at: "2026-10-01T08:00:00Z",
      targets: ["worker1"],
      left_behind: [],
    },
  };
  settings.repair_key = repairKey;
  settings.backup = { ...installBackup, source: { time: "env", keep: "env" } };
  // The users of the mock (D-321): the restore locked all but admin.
  const MOCK_USERS = ["admin", "alice", "bob", "carol"];
  // T-154: the users of D-330. bob is read, carol plan, alice apply.
  const users = new Map<string, User>(
    (
      [
        ["admin", "admin", "system", "2026-11-13T07:58:12Z"],
        ["alice", "apply", "user:admin", "2026-11-13T09:12:00Z"],
        ["bob", "read", "user:admin", null],
        ["carol", "plan", "user:admin", "2026-11-12T16:40:00Z"],
      ] as const
    ).map(([name, role, by, last]) => [
      name,
      {
        name,
        role,
        disabled: false,
        locked: false,
        must_change_password: last === null,
        created_at: "2026-11-10T08:00:00Z",
        created_by: by,
        last_login_at: last,
      },
    ]),
  );
  const restore: RestoreState = opts.restored
    ? structuredClone(example("getRestore", 200))
    : { restored: false, restored_mode: false };
  if (opts.restored) {
    let n = 0;
    for (const t of tokens.values())
      if (!t.revoked_at) {
        t.disabled_at = restore.restored_at ?? now();
        n++;
      }
    restore.disabled_tokens = n;
    restore.locked_users = ["alice", "bob"];
  }
  // T-154: the starter catalogue (D-328) and the signature times of pins (D-335).
  const pinSignedAt = new Map<string, string | null>();
  const catalogFrom = (doc: BlockDoc, description: string, kinds: ("vm" | "lxc")[]) => ({
    name: doc.name,
    title: doc.spec.title ?? doc.name,
    description,
    kinds,
    sha256: mockHash(toYaml(doc)).padEnd(64, "0").slice(0, 64),
    yaml: toYaml(doc),
    document: doc,
  });
  const catalog: Omit<CatalogBlock, "stored">[] = [
    ...exampleItems<CatalogBlock>(example("listCatalogBlocks", 200)).map((c) =>
      catalogFrom(c.document as BlockDoc, c.description, c.kinds),
    ),
    ...["nginx"]
      .map((n) => blocks.get(n))
      .filter((b): b is BlockDoc => !!b)
      .map((b) =>
        catalogFrom(structuredClone(b), "Install Nginx with a default site.", ["vm", "lxc"]),
      ),
    catalogFrom(
      {
        kind: "Block",
        name: "unattended-upgrades",
        spec: {
          title: "Unattended upgrades",
          params: { type: "object", properties: {} },
          resources: [{ package: { name: "unattended-upgrades" } }],
        },
      } as BlockDoc,
      "Install security updates every day.",
      ["vm", "lxc"],
    ),
    catalogFrom(
      {
        kind: "Block",
        name: "sysctl-hardening",
        spec: {
          title: "Kernel hardening",
          params: { type: "object", properties: {} },
          resources: [{ sysctl: { name: "kernel.kptr_restrict", value: "2" } }],
        },
      } as BlockDoc,
      "A few safe kernel settings.",
      ["vm"],
    ),
  ].sort((a, b) => a.name.localeCompare(b.name));

  /** 409 `restored-mode` for the writers that wait for the confirm (D-303). */
  function writable(): void {
    if (restore.restored_mode)
      throw problem(
        "restored-mode",
        "Restored mode",
        409,
        "This Plumb was restored from a backup. Confirm that the old controller is off first.",
      );
  }
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

  /** A person who logged in, not a token. */
  const isPerson = (s: Session) => s.principal.startsWith("user:");

  /** The minimum role of an operation (D-342, from api/openapi.yaml): 403 below it. */
  function need(op: keyof typeof roles): Session {
    const s = auth();
    const order = ["read", "plan", "apply", "admin"];
    const want = roles[op];
    if (want !== "none" && order.indexOf(s.tier) < order.indexOf(want)) {
      throw problem("forbidden", "Forbidden", 403, `This needs the role ${want} or higher.`);
    }
    return s;
  }

  /** An admin session: a person with role admin (D-330). */
  function adminPerson(): Session {
    const s = need("listUsers");
    if (!isPerson(s))
      throw problem("human-required", "Human session required", 403, "Only a person can do this.");
    return s;
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

  /** One page, newest (highest id) first; the mock cursor is the id of the first item. */
  function page<T extends { id: number }>(all: T[], cursor: string | undefined, limit: number) {
    const from = cursor ? Number(cursor) : Infinity;
    const rest = all.filter((x) => x.id <= from).sort((a, b) => b.id - a.id);
    const next = rest[limit];
    return structuredClone({
      items: rest.slice(0, limit),
      next: next ? String(next.id) : null,
    });
  }

  // T-170: the bound of Plan.documents. Past 500 entries or 8 MiB of text
  // the rest keep kind, name and op only.
  const DOC_ENTRIES = 500;
  const DOC_BYTES = 8 << 20;

  /**
   * The Plan with `documents`: what its approval commits, the base
   * revision against the proposal applied (T-170); pending and stale
   * Plans only. A seeded example Plan
   * without a stored proposal leaves the field out, like a server before
   * T-170.
   */
  function withDocuments(p: Plan): Plan {
    const out = structuredClone(p);
    // An approved Plan has no list: its revision diff shows the change.
    if (p.status === "approved") return out;
    if (p.origin === "build" || p.origin === "agent_update") {
      out.documents = [];
      return out;
    }
    const base = p.base_revision === null ? undefined : revisions.get(p.base_revision);
    const before: Snapshot = base?.docs ?? new Map();
    let after: Snapshot | undefined;
    const proposal = pendingDocs.get(p.id);
    if (p.origin === "rollback" && p.rollback_to) {
      after = revisions.get(p.rollback_to)?.docs;
    } else if (proposal) {
      after = new Map(before);
      for (const d of proposal.put ?? []) after.set(docKey(d), d);
      for (const d of proposal.delete ?? []) after.delete(docKey(d));
    }
    if (!after) return out;
    let bytes = 0;
    out.documents = diffDocs(before, after).map((c, i) => {
      bytes += JSON.stringify([c.before, c.after]).length;
      if (i < DOC_ENTRIES && bytes <= DOC_BYTES) return c;
      out.documents_truncated = true;
      return { ...c, before: null, after: null };
    });
    return out;
  }

  function findPlan(id: number): Plan {
    const p = plans.get(id);
    if (!p) throw problem("not-found", "Not found", 404, `Plan ${id} does not exist.`);
    return p;
  }

  function newPlan(
    fields: Pick<Plan, "origin" | "issue_id" | "reason" | "targets" | "warnings"> &
      Partial<Pick<Plan, "human_only" | "rollback_to" | "images" | "pins">>,
  ): Plan {
    const s = auth();
    const all = fields.targets.flatMap((t) =>
      t.resources.flatMap((r) => r.changes.map((c) => c.risk)),
    );
    // A build or a template removal changes no guest: reversible (D-201).
    if (fields.images?.length) all.push("reversible");
    // A pin-only change is reversible (contract example of refreshImagePin).
    if (fields.pins?.length) all.push("reversible");
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

  // --- T-064: image builds (D-197 … D-210) ---

  const findImage = (name: string) => images.find((i) => i.document.name === name);

  /** The recipe hash of a built Image (D-199): base, upgrade, build disk, Blocks. */
  function recipeHash(spec: ImageSpec): string {
    const text = recipeText(spec);
    // The examples name their own hash (Plan 21): an unchanged seed recipe keeps it.
    const seeded = seedRecipes.get(text);
    if (seeded) return seeded;
    let h = "";
    for (let i = 0; h.length < 64; i++) h += mockHash(`${i}:${text}`).padStart(8, "0");
    return h.slice(0, 64);
  }
  function recipeText(spec: ImageSpec): string {
    return JSON.stringify([
      spec.type,
      spec.url,
      spec.sha256,
      spec.build?.upgrade ?? true,
      spec.build?.guest.disk,
      // Defaults are the same recipe: no id = the Block name, no params = {}.
      // A parameter at its default is the same recipe (the server hashes the rendered resources).
      (spec.build?.blocks ?? []).map((b) => {
        const params: Record<string, unknown> = {};
        for (const [k, p] of Object.entries(blocks.get(b.use)?.spec.params?.properties ?? {})) {
          const d = (p as { default?: unknown }).default;
          if (d !== undefined) params[k] = d;
        }
        Object.assign(params, b.params ?? {});
        const sorted = Object.fromEntries(
          Object.entries(params).sort(([x], [y]) => x.localeCompare(y)),
        );
        return [b.use, b.id ?? b.use, sorted];
      }),
    ]);
  }
  const seedRecipes = new Map<string, string>();
  for (const i of images) {
    const newest = i.status?.versions[0];
    if (i.document.spec.build && newest)
      seedRecipes.set(recipeText(i.document.spec), newest.recipe_sha256);
  }

  /** The disk storage now; running builds hold their disk (D-203). */
  function diskNow(): DiskUse {
    const base = images.find((i) => i.status?.disk)?.status?.disk;
    const d: DiskUse = base
      ? structuredClone(base)
      : {
          storage: "local-lvm",
          total_bytes: 0,
          used_bytes: 0,
          reserved_bytes: 0,
          limit_percent: 85,
        };
    d.reserved_bytes =
      images
        .flatMap((i) => i.status?.versions ?? [])
        .filter((v) => v.status === "building")
        .reduce((a, v) => a + v.disk_gb, 0) * GIB;
    return d;
  }

  /** Refuses Blocks with a secret in an Image (D-202): writeOnly parameters or {secret: …}. */
  function checkImageDoc(img: ImageDoc, at: number) {
    const spec = img.spec;
    const fail = (pointer: string, message: string) => {
      throw problem("validation", "Validation failed", 422, message, {
        errors: [{ pointer: `/put/${at}/spec${pointer}`, message }],
      });
    };
    if (!spec.build && (spec.retention || spec.schedule))
      fail("/build", "retention and schedule need build.");
    (spec.build?.blocks ?? []).forEach((use, n) => {
      const block = blocks.get(use.use);
      if (!block) fail(`/build/blocks/${n}/use`, `Block "${use.use}" does not exist.`);
      const props = Object.entries(block?.spec.params?.properties ?? {});
      if (props.some(([, p]) => (p as { writeOnly?: boolean }).writeOnly === true))
        fail(
          `/build/blocks/${n}/use`,
          `Block "${use.use}" has a secret parameter. An Image cannot hold a secret.`,
        );
      if (Object.values(use.params ?? {}).some((v) => secretName(v) !== undefined))
        fail(`/build/blocks/${n}/params`, "An Image cannot hold a secret.");
    });
  }

  const nextVersion = (view: ImageView | undefined) =>
    Math.max(0, ...(view?.status?.versions ?? []).map((v) => v.version)) + 1;

  /** Templates that the retention step removes (D-209). */
  function removals(
    view: ImageView | undefined,
    keep: number,
    building: boolean,
    next?: ImageSpec,
  ): ImagePlan["removes"] {
    const name = view?.document.name ?? "";
    const ready = (view?.status?.versions ?? [])
      .filter((v) => v.status === "ready")
      .sort((a, b) => b.version - a.version);
    const out: ImagePlan["removes"] = ready
      .slice(next ? Math.max(0, keep - (building ? 1 : 0)) : 0)
      .map((v) => ({
        kind: "version",
        name: `plumb-img-${name}-v${v.version}`,
        version: v.version,
      }));
    const old = view?.document.spec;
    if (old && next && old.sha256 !== next.sha256)
      out.push({
        kind: "base",
        name: `plumb-img-${name}-${(old.sha256 ?? "").slice(0, 12)}`,
        version: null,
      });
    return out;
  }

  function buildPlanOf(
    view: ImageView | undefined,
    spec: ImageSpec,
    trigger: ImageVersion["trigger"],
  ): NonNullable<ImagePlan["build"]> {
    const build = spec.build;
    if (!build) throw problem("unprocessable", "Not a built image", 422, "The image has no build.");
    const rendered = render(
      {
        type: "vm",
        image: "",
        guest: build.guest,
        blocks: build.blocks ?? [],
      } as TargetDoc["spec"],
      blocks,
    );
    return {
      version: nextVersion(view),
      trigger,
      upgrade: build.upgrade ?? true,
      recipe_sha256: recipeHash(spec),
      resources: rendered.map((r) => ({
        resource: r.id,
        action: "create",
        risk: "reversible",
        changes: [],
        unverified: true,
      })),
    };
  }

  /** The Image part of a proposal: builds, removals, the human-only reason. */
  function planImages(proposal: Proposal): { images: ImagePlan[]; recipe: boolean } {
    const out: ImagePlan[] = [];
    let recipe = false;
    (proposal.put ?? []).forEach((doc, at) => {
      if (doc.kind !== "Image") return;
      const img = doc as ImageDoc;
      checkImageDoc(img, at);
      const view = findImage(img.name);
      const old = view?.document.spec;
      // D-235: a new lxc Image, or a new url or sha512 of one, is human-only.
      if (
        img.spec.type === "lxc" &&
        (old?.type !== "lxc" || old.url !== img.spec.url || old.sha512 !== img.spec.sha512)
      )
        recipe = true;
      if (!img.spec.build) {
        if (old?.build) {
          recipe = true;
          out.push({ image: img.name, removes: removals(view, 0, false) });
        }
        return;
      }
      const changed = !old?.build || recipeHash(old) !== recipeHash(img.spec);
      if (
        changed ||
        JSON.stringify([old.schedule, old.retention]) !==
          JSON.stringify([img.spec.schedule, img.spec.retention])
      )
        recipe = true;
      const keep = img.spec.retention?.keep ?? 3;
      if (changed) {
        out.push({
          image: img.name,
          build: buildPlanOf(view, img.spec, "change"),
          removes: removals(view, keep, true, img.spec),
          disk: diskNow(),
        });
      } else {
        const removes = removals(view, keep, false, img.spec);
        if (removes.length) out.push({ image: img.name, removes });
      }
    });
    for (const ref of proposal.delete ?? []) {
      if (ref.kind !== "Image") continue;
      const user = [...targets.values()].find((t) => t.document.spec.image === ref.name);
      if (user)
        throw problem(
          "validation",
          "Validation failed",
          422,
          `Target "${user.document.name}" uses image "${ref.name}".`,
        );
      const view = findImage(ref.name);
      if (view?.document.spec.build) {
        recipe = true;
        out.push({ image: ref.name, removes: removals(view, 0, false) });
      }
    }
    return { images: out, recipe };
  }

  /** The Target part of D-202: a smaller disk than the template, or no ready version yet. */
  function checkTargetImage(t: TargetDoc, at: number, isNew: boolean) {
    if (t.spec.image === undefined) return; // adopted (D-386)
    const view = findImage(t.spec.image);
    if (
      view?.document.spec.type === "lxc" &&
      view.document.spec.build &&
      t.spec.guest.ssh_keys?.length
    ) {
      const message = `a container from the built Image "${t.spec.image}" gets no SSH keys from Proxmox; add them with a Block such as deploy-user (D-251)`;
      throw problem("validation", "Validation failed", 422, message, {
        errors: [{ pointer: `/put/${at}/spec/guest/ssh_keys`, message }],
      });
    }
    const min = view?.document.spec.build?.guest.disk;
    if (min !== undefined && t.spec.guest.disk < min) {
      const message = `The disk must be at least ${min} GiB: the template of image ${t.spec.image} has ${min} GiB, and a disk cannot shrink.`;
      throw problem("validation", "Validation failed", 422, message, {
        errors: [{ pointer: `/put/${at}/spec/guest/disk`, message }],
      });
    }
    if (
      isNew &&
      view?.document.spec.build &&
      !view.status?.versions.some((v) => v.status === "ready")
    )
      throw problem(
        "validation",
        "Validation failed",
        422,
        `Image ${t.spec.image} has no ready version. Build the image first.`,
      );
  }

  function imageLanes(p: Plan): ImageRun[] {
    return (p.images ?? []).flatMap((ip) => {
      if (!ip.build) return [];
      const steps = [
        "image:base",
        "guest:config",
        ...ip.build.resources.map((r) => r.resource),
        "image:seal",
        "image:template",
        "image:retention",
      ];
      return [
        {
          image: ip.image,
          version: Math.max(ip.build.version, nextVersion(findImage(ip.image))),
          trigger: ip.build.trigger,
          status: "running" as const,
          steps: steps.map((resource) => ({
            resource,
            action:
              resource.startsWith("image:") || resource === "guest:config"
                ? null
                : ("create" as const),
            status: "pending" as const,
            changes: [],
            output: "",
            started_at: null,
            finished_at: null,
          })),
        },
      ];
    });
  }

  /** Removes templates: the version rows become `removed` (D-209). */
  function retain(ip: ImagePlan) {
    const view = findImage(ip.image);
    for (const r of ip.removes) {
      const v = view?.status?.versions.find((x) => x.version === r.version);
      if (v && v.status === "ready") {
        v.status = "removed";
        v.removed_at = now();
      }
    }
  }

  /** One build lane: version row, steps, the disk check, retention. */
  async function buildLane(run: Run, lane: ImageRun, plan: Plan): Promise<boolean> {
    const view = findImage(lane.image);
    const spec = view?.document.spec;
    if (!view || !spec?.build) {
      lane.status = "failed";
      return false;
    }
    view.status ??= {
      versions: [],
      building: null,
      last_build: null,
      next_build_at: null,
      unused_imports: [],
    };
    const st = view.status;
    const free = diskNow();
    const v: ImageVersion = {
      version: lane.version,
      status: "building",
      trigger: lane.trigger,
      run_id: run.id,
      revision: run.revision,
      recipe_sha256: recipeHash(spec),
      // D-254: the SHA-512 pin of an lxc base, the SHA-256 of a vm base.
      base_sha256: (spec.type === "lxc" ? spec.sha512 : spec.sha256) ?? "",
      disk_gb: spec.build.guest.disk,
      started_at: now(),
      finished_at: null,
      removed_at: null,
      targets: [],
    };
    st.versions.unshift(v);
    st.building = v.version;
    emit("image.version", `image:${lane.image}`, { version: v.version, status: v.status });
    const finish = (status: "ready" | "failed") => {
      v.status = status;
      v.finished_at = now();
      st.building = null;
      st.last_build = structuredClone(v);
      lane.status = status === "ready" ? "succeeded" : "failed";
      emit("image.version", `image:${lane.image}`, { version: v.version, status });
    };
    for (const step of lane.steps) {
      await sleep(stepDelay);
      step.status = "running";
      step.started_at = now();
      emit("run.step", `run:${run.id}`, {
        image: lane.image,
        resource: step.resource,
        status: "running",
      });
      await sleep(stepDelay);
      step.finished_at = now();
      if (step.resource === "guest:config") {
        const use = diskUse(free, v.disk_gb);
        if (use.over) {
          step.status = "failed";
          step.output = diskRefusal(free.storage, use.after, free.limit_percent);
          for (const rest of lane.steps) if (rest.status === "pending") rest.status = "skipped";
          emit("run.step", `run:${run.id}`, {
            image: lane.image,
            resource: step.resource,
            status: "failed",
          });
          finish("failed");
          return false;
        }
      }
      if (step.resource === "image:template") step.output = `plumb-img-${lane.image}-v${v.version}`;
      if (step.resource === "image:seal") step.output = "10 steps, guest powered off";
      const ip = plan.images?.find((x) => x.image === lane.image);
      if (step.resource === "image:retention" && ip) retain(ip);
      step.status =
        step.resource === "image:base" ||
        (step.resource === "image:retention" && !ip?.removes.length)
          ? "unchanged"
          : "applied";
      emit("run.step", `run:${run.id}`, {
        image: lane.image,
        resource: step.resource,
        status: step.status,
      });
    }
    finish("ready");
    return true;
  }

  /** Puts or deletes Image documents after an approved proposal. */
  function applyImageDocs(proposal: Proposal | undefined) {
    for (const ref of proposal?.delete ?? []) {
      if (ref.kind !== "Image") continue;
      const i = images.findIndex((x) => x.document.name === ref.name);
      if (i >= 0) images.splice(i, 1);
    }
    for (const doc of proposal?.put ?? []) {
      if (doc.kind !== "Image") continue;
      const img = doc as ImageDoc;
      let view = findImage(img.name);
      if (!view) {
        view = { revision: head, document: img };
        images.push(view);
      }
      view.revision = head;
      view.document = img;
      // A stock vm Image has no status; an lxc Image has one for its
      // unused template files (D-233).
      if (!img.spec.build && img.spec.type !== "lxc") {
        delete view.status;
        continue;
      }
      view.status ??= {
        versions: [],
        building: null,
        last_build: null,
        next_build_at: null,
        unused_imports: [],
      };
      view.status.next_build_at = img.spec.schedule
        ? nextSlot(img.spec.schedule, new Date()).toISOString()
        : null;
    }
  }

  /** Approval of a build Plan: no revision; stale when the recipe moved (D-199). */
  function approveBuild(p: Plan, s: Session): Run {
    for (const ip of p.images ?? []) {
      const view = findImage(ip.image);
      if (
        !view?.document.spec.build ||
        recipeHash(view.document.spec) !== ip.build?.recipe_sha256
      ) {
        p.status = "stale";
        emit("plan.stale", `plan:${p.id}`, {});
        throw problem(
          "stale",
          "Plan is stale",
          409,
          `The recipe of image ${ip.image} changed. Make a new plan.`,
        );
      }
      if (view.status?.building)
        throw problem(
          "build-running",
          "Build running",
          409,
          `Image ${ip.image} builds version ${view.status.building} now. Wait until it ends.`,
        );
    }
    p.status = "approved";
    p.approved_by = s.principal;
    p.approved_at = now();
    const run: Run = {
      id: nextRun++,
      plan_id: p.id,
      revision: head,
      status: "running",
      waits_for_restart: 0,
      warnings: 0,
      started_at: now(),
      finished_at: null,
      targets: [],
      images: imageLanes(p),
    };
    p.run_id = run.id;
    runs.set(run.id, run);
    emit("plan.approved", `plan:${p.id}`, { run_id: run.id });
    const job = simulate(run, p, () => {});
    active.add(job);
    void job.finally(() => active.delete(job));
    return structuredClone(run);
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
    let ok = true;
    for (const lane of run.images ?? []) ok = (await buildLane(run, lane, plan)) && ok;
    // A Plan that only lowers keep or deletes an Image removes templates here.
    for (const ip of plan.images ?? []) if (!ip.build) retain(ip);
    run.status = ok ? "succeeded" : "failed";
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
    async getImage(name) {
      auth();
      const i = findImage(name);
      if (!i) throw new ApiError(example("getImage", 404));
      const out = structuredClone(i);
      if (out.status) out.status.disk = diskNow();
      return out;
    },
    async buildImage(name, reason) {
      need("buildImage");
      writable();
      const view = findImage(name);
      if (!view) throw new ApiError(example("getImage", 404));
      if (!view.document.spec.build)
        throw problem(
          "unprocessable",
          "Not a built image",
          422,
          `Image ${name} is a stock image. Add a build to it first.`,
        );
      return newPlan({
        origin: "build",
        issue_id: null,
        reason: reason?.trim() || `Build image ${name}.`,
        targets: [],
        warnings: [],
        images: [
          {
            image: name,
            build: buildPlanOf(view, view.document.spec, "manual"),
            removes: removals(
              view,
              view.document.spec.retention?.keep ?? 3,
              true,
              view.document.spec,
            ),
            disk: diskNow(),
          },
        ],
      });
    },
    async listBlocks() {
      auth();
      return structuredClone(blockViews);
    },
    async getBlock(name) {
      auth();
      const b = blockViews.find((v) => v.document.name === name);
      if (!b) throw problem("not-found", "Not found", 404, `Block "${name}" does not exist.`);
      return structuredClone(b);
    },
    async listCatalogBlocks() {
      auth();
      return structuredClone(
        catalog.map((c) => {
          const stored = blocks.get(c.name);
          return {
            ...c,
            stored: !stored ? "none" : toYaml(stored) === toYaml(c.document) ? "same" : "differs",
          } satisfies CatalogBlock;
        }),
      );
    },
    async refreshImagePin(name) {
      const s = need("refreshImagePin");
      writable();
      const view = findImage(name);
      if (!view) throw new ApiError(example("getImage", 404));
      const spec = view.document.spec;
      const source = spec.verify?.source;
      if (!source)
        throw problem("unprocessable", "No verify", 422, `Image ${name} has no verify source.`);
      const algorithm = spec.sha512 ? "sha512" : "sha256";
      const old = (algorithm === "sha512" ? spec.sha512 : spec.sha256) ?? null;
      const signed = source !== "debian-https";
      // The "vendor list" of the mock: one newer pin per Image, then current.
      const fresh = [0, 1, 2, 3, 4, 5, 6, 7, 8]
        .map((i) => mockHash(`${name}:${spec.url}:next:${i}`))
        .join("")
        .replace(/[^0-9a-f]/g, "")
        .padEnd(64, "0")
        .slice(0, 64);
      const next = algorithm === "sha512" ? fresh + fresh : fresh;
      const last = pinSignedAt.get(name) ?? null;
      const pin: PinRefresh = {
        image: name,
        source,
        signed,
        key_fingerprint: signed
          ? source === "ubuntu-cloud"
            ? "D2EB44626FDDC30B513D5BB71A5D6C4C7DB87C81"
            : "24B30F06ECC1836A4E5EFECBA7BCD1420BFE778E"
          : null,
        file: spec.url.split("/").pop() ?? "",
        algorithm,
        old,
        new: next,
        used_before: false,
        signed_at: signed ? "2026-11-12T04:11:09Z" : null,
        last_signed_at: last,
      };
      if (old === next) return { unchanged: { ...pin, used_before: true } };
      const doc = structuredClone(view.document);
      if (algorithm === "sha512") doc.spec.sha512 = next;
      else doc.spec.sha256 = next;
      const p = newPlan({
        origin: "pin_refresh",
        issue_id: null,
        reason: `Refresh the base pin of ${name} from ${source}.`,
        targets: [],
        warnings: [],
        human_only: !signed || last === null ? ["unverified-pin"] : [],
        pins: [pin],
      });
      void s;
      pendingDocs.set(p.id, { reason: p.reason, put: [doc] });
      if (signed) pinSignedAt.set(name, pin.signed_at);
      return { plan: withDocuments(p) };
    },
    async listTargets() {
      auth();
      return structuredClone([...targets.values()].map(withRaw));
    },
    async getTarget(name) {
      auth();
      const t = targets.get(name);
      if (!t) throw new ApiError(example("getTarget", 404));
      return structuredClone(withRaw(t));
    },
    async createPlan(proposal: Proposal) {
      need("createPlan");
      if (!proposal.reason.trim()) {
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [{ pointer: "/reason", message: "A reason is required." }],
        });
      }
      const planTargets: Plan["targets"] = [];
      const humanOnly = new Set<NonNullable<Plan["human_only"]>[number]>();
      const warnings: string[] = [];
      const imagePart = planImages(proposal);
      if (imagePart.recipe) humanOnly.add("image-recipe");
      // Blocks: the schema check of the server (block.json), then the next set (T-096).
      const nextBlocks = new Map(blocks);
      for (const [at, doc] of (proposal.put ?? []).entries()) {
        if (doc.kind !== "Block") continue;
        const errs = checkBlock(doc);
        if (errs.length) {
          throw problem("validation", "Validation failed", 422, "The documents are not valid.", {
            errors: errs.map((e) => ({
              pointer: `/put/${at}${e.pointer}`,
              message: `Block "${doc.name}": ${e.message}`,
            })),
          });
        }
        nextBlocks.set(doc.name, doc as BlockDoc);
      }
      for (const [at, doc] of (proposal.put ?? []).entries()) {
        if (doc.kind !== "Target") continue;
        const t = doc as TargetDoc;
        if (t.spec.image === undefined) continue; // an adopted Target has no image (D-386)
        const known =
          images.some((i) => i.document.name === t.spec.image) ||
          (proposal.put ?? []).some((d) => d.kind === "Image" && d.name === t.spec.image);
        if (known) checkTargetImage(t, at, !targets.has(t.name));
        if (!known) {
          throw problem(
            "validation",
            "Validation failed",
            422,
            `Image "${t.spec.image}" does not exist.`,
          );
        }
        const old = targets.get(t.name)?.document.spec;
        let resources = planTarget(
          old,
          t.spec,
          nextBlocks,
          targets.get(t.name)?.status.power,
          blocks,
        );
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
      // A deleted Block must not be in use by a Target or Image that stays (server: 422).
      for (const ref of proposal.delete ?? []) {
        if (ref.kind === "Block") {
          if (!blocks.has(ref.name))
            throw problem(
              "validation",
              "Validation failed",
              422,
              `Block "${ref.name}" does not exist and cannot be deleted.`,
            );
          const gone = new Set(
            (proposal.delete ?? []).filter((d) => d.kind === "Target").map((d) => d.name),
          );
          const u = usersOf(
            ref.name,
            [...targets.values()].filter((t) => !gone.has(t.document.name)),
            images,
          );
          const first = u.targets[0] ?? u.images[0];
          if (first)
            throw problem("validation", "Validation failed", 422, "The documents are not valid.", {
              errors: [
                { pointer: "/delete", message: `Block "${ref.name}" is used by "${first}".` },
              ],
            });
        }
        if (ref.kind !== "Target") continue;
        const t = targets.get(ref.name);
        if (!t)
          throw problem(
            "validation",
            "Validation failed",
            422,
            `Target "${ref.name}" does not exist and cannot be deleted.`,
          );
        planTargets.push({
          target: ref.name,
          resources: [
            resourcePlan("guest:config", "delete", [
              { attr: "state", from: "present", to: "absent", risk: "destructive" },
            ]),
          ],
        });
      }
      // Every other Target whose render changes with the Blocks is touched (T-096).
      for (const t of targets.values()) {
        if (planTargets.some((p) => p.target === t.document.name)) continue;
        if ((proposal.delete ?? []).some((d) => d.kind === "Target" && d.name === t.document.name))
          continue;
        const spec = t.document.spec;
        if (same(render(spec, blocks), render(spec, nextBlocks))) continue;
        const used = plannedSecrets(spec);
        planTargets.push({
          target: t.document.name,
          ...(used.length ? { secrets: used } : {}),
          resources: planTarget(spec, spec, nextBlocks, t.status.power, blocks),
        });
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
      // The gate (D-259): a Target whose agent lacks what its state needs.
      // A deleted Target renders nothing in the new state: no gate (server checkGate).
      const deleted = new Set(
        (proposal.delete ?? []).filter((d) => d.kind === "Target").map((d) => d.name),
      );
      const outdated = planTargets
        .filter((p) => !deleted.has(p.target))
        .map((p) => targets.get(p.target))
        .filter((t): t is TargetView => t?.status.agent_update === "required");
      if (outdated.length) {
        throw problem(
          "agent-outdated",
          "Agent too old",
          409,
          "update the agent first: the desired state needs what the agent does not know.",
          {
            targets: outdated.map((t) => t.document.name),
            missing: [...new Set(outdated.flatMap((t) => t.status.agent_missing ?? []))].sort(),
          },
        );
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
        ...(imagePart.images.length ? { images: imagePart.images } : {}),
      });
      // Kept as the server keeps it: a copy of the JSON body, not the
      // caller's objects (a screen may pass a Svelte state proxy).
      pendingDocs.set(p.id, JSON.parse(JSON.stringify(proposal)) as Proposal);
      return withDocuments(p);
    },
    async getPlan(id) {
      auth();
      return withDocuments(findPlan(id));
    },
    async approvePlan(id) {
      const s = auth();
      writable();
      const p = findPlan(id);
      // D-329: a person with role apply approves up to disruptive; destructive needs admin.
      if (p.status === "pending" && isPerson(s) && s.tier === "apply" && p.risk === "destructive")
        throw problem("forbidden", "Forbidden", 403, "A destructive plan needs the role admin.");
      // Only a person approves a human-only Plan (D-099; QA T-036 F2).
      if (p.status === "pending" && (p.human_only?.length ?? 0) > 0 && !isPerson(s)) {
        throw problem(
          "human-required",
          "Human session required",
          403,
          "Only a person can approve this plan.",
        );
      }
      need("approvePlan");
      if (p.status === "pending" && p.origin === "build") return approveBuild(p, s);
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
          // D-128: the author of the Plan, not the approver (approved_by).
          author: p.author,
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
        warnings: 0,
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
      const lanes = imageLanes(p);
      if (lanes.length) run.images = lanes;
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
        // T-097: the updated agent says hello with the served binary.
        if (p.origin === "agent_update") {
          for (const tp of p.targets) {
            const t = targets.get(tp.target);
            if (t)
              Object.assign(t.status, {
                agent_version: MOCK_AGENT_VERSION,
                agent_update: "none",
                agent_missing: [],
                agent_reverted: null,
              });
          }
        }
        applyImageDocs(proposal);
        for (const ref of proposal?.delete ?? []) {
          if (ref.kind === "Target") targets.delete(ref.name);
          if (ref.kind === "Block") {
            blocks.delete(ref.name);
            const at = blockViews.findIndex((v) => v.document.name === ref.name);
            if (at >= 0) blockViews.splice(at, 1);
          }
        }
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
        // T-194: a repair brings the agent back; an adopted guest has no agent yet.
        for (const tp of p.targets) {
          const t = targets.get(tp.target);
          if (!t) continue;
          if (p.origin === "repair") {
            Object.assign(t.status, { agent: "online", last_seen: now() });
            emit("repair.finished", `target:${t.document.name}`, { result: "succeeded" });
          }
          const g = p.origin === "adopt" ? adoptions.get(p.id) : undefined;
          if (g) {
            Object.assign(t.status, {
              agent: "none",
              power: g.power ?? "unknown",
              guest_id: String(g.vmid),
              repair: { state: "unknown", reasons: [] },
            });
            const at = unmanaged.indexOf(g);
            if (at >= 0) unmanaged.splice(at, 1);
          }
        }
      });
      active.add(job);
      void job.finally(() => active.delete(job));
      return structuredClone(run);
    },
    async listPlans(filter, cursor, limit = 50) {
      auth();
      return page(
        // Plan.documents is only in answers with one Plan (T-170).
        [...plans.values()].filter((p) => !filter?.status || p.status === filter.status),
        cursor,
        limit,
      );
    },
    async discardPlan(id) {
      need("discardPlan");
      const p = findPlan(id);
      if (p.status !== "pending")
        throw problem("conflict", "Plan is not pending", 409, `Plan ${id} is ${p.status}.`);
      p.status = "stale";
      const issue = p.issue_id ? issues.get(p.issue_id) : undefined;
      if (issue?.status === "resolving" && issue.plan_id === p.id) {
        issue.status = "open";
        issue.plan_id = null;
        issue.actions = ["accept", "revert", "ignore_once", "ignore_rule"];
        emit("issue.updated", `issue:${issue.id}`, {});
      }
      emit("plan.stale", `plan:${p.id}`, {});
    },
    async listRuns(cursor, limit = 50) {
      auth();
      return page([...runs.values()], cursor, limit);
    },
    async commentIssue(id, body) {
      const s = need("commentIssue");
      const issue = issues.get(id);
      if (!issue) throw new ApiError(example("getIssue", 404));
      if (!body.trim() || body.length > 10000)
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [{ pointer: "/body", message: "A comment needs 1 to 10000 characters." }],
        });
      const c = {
        id: Math.max(0, ...[...issues.values()].flatMap((i) => i.comments.map((x) => x.id))) + 1,
        author: s.principal,
        body,
        created_at: now(),
      };
      issue.comments.push(c);
      emit("issue.updated", `issue:${issue.id}`, {});
      return structuredClone(c);
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
      return structuredClone(list.sort((a, b) => b.id - a.id).map(withSource));
    },
    async getIssue(id) {
      auth();
      const i = issues.get(id);
      if (!i) throw new ApiError(example("getIssue", 404));
      return structuredClone(withSource(i));
    },
    async resolveIssue(id, action, reason, rule) {
      need("resolveIssue");
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
          // D-323: an Accept adopts guest data; only a person approves it.
          human_only: action === "accept" ? ["accept-guest-data"] : [],
          warnings:
            action === "accept"
              ? [
                  withSource(issue).accept_mode === "param"
                    ? "Accept writes the guest value into a Block parameter of the Target. The guest does not change."
                    : "Accept writes the guest value into the Target as an override. The guest does not change.",
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
      const s = need("resolveIssue");
      const issue = issues.get(id);
      if (!issue) throw new ApiError(example("getIssue", 404));
      // A person with role apply up to disruptive (ARCHITECTURE §17).
      if (isPerson(s) && s.tier !== "admin") {
        if (s.tier !== "apply" || issue.risk === "destructive")
          throw problem("forbidden", "Forbidden", 403, "This needs the role apply or admin.");
      }
      // A person, or an apply token within its max risk and without sensitive changes (D-091).
      if (!isPerson(s) && s.tier !== "admin") {
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
    async updateTargetAgent(name, reason) {
      auth();
      const t = targets.get(name);
      if (!t) throw problem("not-found", "Not found", 404, `Target ${name} does not exist.`);
      const u = t.status.agent_update;
      if (u === "none") {
        throw problem("nothing-to-do", "Nothing to do", 409, `The agent of ${name} is current.`);
      }
      if (u === "manual" || u === "unknown") {
        throw problem(
          "validation",
          "Validation failed",
          422,
          u === "manual"
            ? `The agent of ${name} is older than slice 3: run the one-time install in the guest.`
            : `The agent of ${name} has not said hello yet.`,
        );
      }
      return newPlan({
        origin: "agent_update",
        issue_id: null,
        reason: reason || `update the agent of ${name}`,
        targets: [
          {
            target: name,
            resources: [
              resourcePlan("agent:binary", "update", [
                {
                  attr: "version",
                  from: t.status.agent_version,
                  to: MOCK_AGENT_VERSION,
                  risk: "disruptive",
                },
              ]),
            ],
          },
        ],
        warnings: [],
      });
    },
    async reenrollTarget(name, reason) {
      auth();
      writable();
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
      need("rollbackRevision");
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
    async enableToken(id) {
      human();
      const t = tokens.get(id);
      if (!t) throw problem("not-found", "Not found", 404, `Token ${id} does not exist.`);
      if (t.revoked_at)
        throw problem("token-revoked", "Token revoked", 409, `Token ${id} is revoked.`);
      if (t.disabled_at) {
        t.disabled_at = null;
        restore.disabled_tokens = Math.max(0, (restore.disabled_tokens ?? 1) - 1);
        emit("token.enabled", `token:${id}`, {});
      }
      return structuredClone(t);
    },
    async repairTarget(name, req) {
      need("repairTarget");
      writable();
      const t = targets.get(name);
      if (!t) throw new ApiError(example("getTarget", 404));
      const offlineFor = Date.parse(now()) - Date.parse(t.status.last_seen ?? now());
      if (t.status.agent === "online" || t.status.power !== "running" || offlineFor < 10 * 60_000)
        throw problem(
          "repair-not-needed",
          "Repair not needed",
          409,
          t.status.agent === "online"
            ? `The agent of ${name} is online.`
            : t.status.power !== "running"
              ? `The guest of ${name} is not running.`
              : `The agent of ${name} has been offline for less than 10 minutes.`,
        );
      const r = t.status.repair;
      if (r?.state !== "ready")
        throw problem(
          "repair-not-ready",
          "Repair not ready",
          409,
          `${name} has no SSH repair way (${(r?.reasons ?? ["unknown"]).join(", ")}). Use an install command.`,
        );
      const accept = req?.accept_host_key;
      if (accept && !(r.host_keys_seen ?? []).includes(accept))
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [
            { pointer: "/accept_host_key", message: "This host key is not reported as new." },
          ],
        });
      const p = newPlan({
        origin: "repair",
        issue_id: null,
        reason: req?.reason?.trim() || `Repair the agent of ${name}.`,
        targets: [{ target: name, resources: [] }],
        warnings: accept
          ? [`After your approval the Run pins the new host key ${accept} of ${name}.`]
          : [],
        human_only: ["repair"],
      });
      const stored = plans.get(p.id);
      if (stored) stored.risk = "disruptive";
      return withDocuments({ ...p, risk: "disruptive" });
    },
    async createInstallCommand(name) {
      adminPerson();
      writable();
      if (!targets.has(name)) throw new ApiError(example("getTarget", 404));
      emit("target.install_command", `target:${name}`, {});
      // A mock token: random each time, never a real credential.
      const token = Array.from({ length: 6 }, () => Math.random().toString(36).slice(2, 8)).join(
        "",
      );
      return {
        command: `umask 077; mkdir -p /etc/plumb-agent && cat > /etc/plumb-agent/ca.pem <<'PLUMB_CA'\n-----BEGIN CERTIFICATE-----\n(mock CA)\n-----END CERTIFICATE-----\nPLUMB_CA\ncat > /etc/plumb-agent/enroll.token <<'PLUMB_TOKEN'\n${token}\nPLUMB_TOKEN\ncurl -fsS --proto =https --connect-timeout 10 --max-time 300 --capath /etc/plumb-agent --cacert /etc/plumb-agent/ca.pem 'https://plumb.lan:8444/agent/v1/install.sh' | PLUMB_URL='https://plumb.lan:8444' sh\n`,
        expires_at: new Date(Date.parse(now()) + 15 * 60_000).toISOString().replace(/\.\d+Z$/, "Z"),
      };
    },
    async listUnmanagedGuests() {
      need("listUnmanagedGuests");
      return structuredClone(unmanaged);
    },
    async forgetAgent(vmid) {
      need("forgetAgent");
      const g = unmanaged.find((x) => x.vmid === vmid);
      if (!g || !g.agent_left)
        throw problem("not-found", "Not found", 404, `Guest ${vmid} has no agent left.`);
      g.agent_left = false;
    },
    async adoptTarget(name, req) {
      need("adoptTarget");
      writable();
      const g = unmanaged.find((x) => x.node === req.node && x.vmid === req.vmid);
      if (!g) throw problem("not-found", "Not found", 404, `Guest ${req.vmid} is not in the pool.`);
      if (!g.adoptable)
        throw problem(
          "not-adoptable",
          "Not adoptable",
          409,
          `Guest ${req.vmid}: ${g.reasons.join(", ")}.`,
        );
      if (g.digest !== req.digest)
        throw problem(
          "guest-changed",
          "Guest changed",
          409,
          `The config of guest ${req.vmid} changed. Reload the list.`,
        );
      // D-392: the Target has the Proxmox name of its guest.
      if (name !== g.name)
        throw problem(
          "guest-changed",
          "Guest changed",
          409,
          `The Target name must be the Proxmox name of guest ${g.vmid} ("${g.name}").`,
        );
      if (targets.has(name))
        throw problem("document-exists", "Document exists", 409, `A Target "${name}" exists.`);
      const doc = {
        kind: "Target",
        name,
        spec: {
          type: g.type,
          guest: structuredClone(g.guest),
          adopted: { node: g.node, vmid: g.vmid, delete_guest: false },
        },
      } as unknown as TargetDoc;
      const p = newPlan({
        origin: "adopt",
        issue_id: null,
        reason: req.reason?.trim() || `Adopt guest ${g.vmid} (${g.name}).`,
        targets: [{ target: name, resources: [] }],
        warnings: [
          `The guest ${g.vmid} is not sealed: its SSH host keys, machine-id and root users stay as they are.`,
        ],
        human_only: ["accept-guest-data"],
      });
      const stored = plans.get(p.id);
      if (stored) stored.risk = "reversible";
      pendingDocs.set(p.id, { reason: p.reason, put: [doc as StateDocument] });
      adoptions.set(p.id, g);
      return withDocuments({ ...p, risk: "reversible" });
    },
    async rotateRepairKey(revokeOld) {
      adminPerson();
      writable();
      if (revokeOld) {
        if (!repairKey.previous)
          throw problem("nothing-to-revoke", "Nothing to revoke", 409, "There is no previous key.");
        repairKey.previous = null;
      } else {
        if (repairKey.previous)
          throw problem(
            "rotation-running",
            "Rotation running",
            409,
            "Revoke the previous key first.",
          );
        repairKey.previous = {
          fingerprint: repairKey.fingerprint,
          created_at: repairKey.created_at,
          targets: [...targets.values()]
            .filter((t) => t.status.repair && t.status.repair.state !== "unknown")
            .map((t) => t.document.name),
          left_behind: [],
        };
        repairKey.fingerprint = `SHA256:${"n".repeat(43)}`;
        repairKey.created_at = now();
      }
      settings.repair_key = repairKey;
      emit("repair_key.rotated", "settings:repair_key", { revoke_old: revokeOld === true });
      return structuredClone(repairKey);
    },
    async getSettings() {
      const s = need("getSettings");
      // The Proxmox view is for a person only (review n2 of #276).
      return structuredClone({ ...settings, proxmox: isPerson(s) ? settings.proxmox : null });
    },
    async changeSettings(change) {
      adminPerson();
      writable();
      const b = change.backup ?? {};
      const errors: { pointer: string; message: string }[] = [];
      if (
        b.time !== undefined &&
        b.time !== null &&
        !/^([01][0-9]|2[0-3]):[0-5][0-9]$/.test(b.time)
      )
        errors.push({ pointer: "/backup/time", message: "Give a time as HH:MM (UTC)." });
      if (
        b.keep !== undefined &&
        b.keep !== null &&
        !(Number.isInteger(b.keep) && b.keep >= 1 && b.keep <= 100)
      )
        errors.push({ pointer: "/backup/keep", message: "Keep 1 to 100 backups." });
      if (errors.length)
        throw problem("validation", "Validation failed", 422, undefined, { errors });
      const old = structuredClone(settings.backup);
      if (b.time !== undefined) {
        settings.backup.time = b.time ?? installBackup.time;
        settings.backup.source.time = b.time === null ? "env" : "setting";
      }
      if (b.keep !== undefined) {
        settings.backup.keep = b.keep ?? installBackup.keep;
        settings.backup.source.keep = b.keep === null ? "env" : "setting";
      }
      backups.schedule = { time: settings.backup.time, keep: settings.backup.keep };
      emit("settings.changed", "settings:backup", { old, new: settings.backup });
      return structuredClone(settings);
    },
    async listBackups() {
      auth();
      return structuredClone(backups);
    },
    async createBackup() {
      const s = auth();
      writable();
      if (backups.running)
        throw problem("backup-running", "Backup running", 409, "A backup runs already.");
      const run: BackupRun = {
        started_at: now(),
        finished_at: null,
        result: "running",
        by: s.principal,
        name: null,
        message: "",
      };
      backups.running = true;
      backups.last_run = run;
      void sleep(stepDelay * 2).then(() => {
        const at = now();
        const name = `plumb-${at.replace(/[-:]/g, "").replace(/\.\d+Z$/, "Z")}.pbk`;
        backups.items.unshift({
          name,
          created_at: at,
          size: 2621440 + backups.items.length * 4096,
          sha256: "0".repeat(56) + String(backups.items.length).padStart(8, "0"),
        });
        backups.items = backups.items.slice(0, backups.schedule.keep);
        backups.running = false;
        backups.last_run = { ...run, finished_at: at, result: "ok", name };
        emit("backup.finished", `backup:${name}`, { result: "ok" });
      });
      return structuredClone(run);
    },
    async getRestore() {
      auth();
      return structuredClone(restore);
    },
    async confirmRestore(reason) {
      const s = human();
      void reason;
      if (!restore.restored_mode)
        throw problem("nothing-to-do", "Nothing to do", 409, "This Plumb is not in restored mode.");
      restore.restored_mode = false;
      restore.confirmed_at = now();
      restore.confirmed_by = s.principal;
      emit("restore.confirmed", "restore:1", {});
      return structuredClone(restore);
    },
    async listUsers() {
      adminPerson();
      return structuredClone([...users.values()].sort((a, b) => a.name.localeCompare(b.name)));
    },
    async createUser(name, role) {
      const s = adminPerson();
      if (!/^[a-z_][a-z0-9_-]{0,31}$/.test(name))
        throw problem("validation", "Validation failed", 422, undefined, {
          errors: [
            {
              pointer: "/name",
              message: "Use 1 to 32 characters: a-z, 0-9, _ and -; start with a letter or _.",
            },
          ],
        });
      if (users.has(name))
        throw problem("user-exists", "User exists", 409, `The name ${name} is taken.`);
      users.set(name, {
        name,
        role,
        disabled: false,
        locked: false,
        must_change_password: true,
        created_at: now(),
        created_by: s.principal,
        last_login_at: null,
      });
      MOCK_USERS.push(name);
      emit("user.created", `user:${name}`, { role });
      const rnd = crypto.getRandomValues(new Uint8Array(16));
      return {
        username: name,
        password: btoa(String.fromCharCode(...rnd)).replace(/[^A-Za-z0-9]/g, ""),
      };
    },
    async changeUser(name, change) {
      const s = adminPerson();
      const u = users.get(name);
      if (!u) throw problem("not-found", "Not found", 404, `User ${name} does not exist.`);
      if (`user:${name}` === s.principal)
        throw problem(
          "own-user",
          "Own user",
          409,
          "You cannot change your own role or disable yourself.",
        );
      const after = { ...u, ...change };
      const admins = [...users.values()].filter(
        (x) => x.name !== name && x.role === "admin" && !x.disabled,
      );
      if (
        u.role === "admin" &&
        !u.disabled &&
        (after.role !== "admin" || after.disabled) &&
        !admins.length
      )
        throw problem("last-admin", "Last admin", 409, "This is the last enabled admin.");
      Object.assign(u, change);
      emit("user.changed", `user:${name}`, { role: u.role, disabled: u.disabled });
      return structuredClone(u);
    },
    async resetUserPassword(name) {
      const s = human();
      if (`user:${name}` === s.principal)
        throw problem("own-user", "Own user", 409, "Change your own password in your session.");
      if (!MOCK_USERS.includes(name))
        throw problem("not-found", "Not found", 404, `User ${name} does not exist.`);
      const rnd = crypto.getRandomValues(new Uint8Array(16));
      const password = btoa(String.fromCharCode(...rnd)).replace(/[^A-Za-z0-9]/g, "");
      if (restore.locked_users)
        restore.locked_users = restore.locked_users.filter((u) => u !== name);
      emit("user.password_reset", `user:${name}`, {});
      return { username: name, password };
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
  /** T-194: the guest of each adopt Plan. */
  const adoptions = new Map<number, UnmanagedGuest>();

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

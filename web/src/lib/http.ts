// The PlumbApi over HTTP: /api/v1 of the controller (T-014). Same origin,
// session cookie, no token or password is stored by the UI. Errors are
// ApiError with the problem+json body.
import { API_BASE, ApiError, NetworkError, toProblem } from "./api";
import type { PlumbApi } from "./client";
import type { components } from "./api.gen";
import type {
  BlockView,
  ImageView,
  Issue,
  PageOf,
  Plan,
  PlumbEvent,
  RevisionDiff,
  RevisionInfo,
  RuleView,
  Run,
  SecretInfo,
  Session,
  TargetView,
  Token,
  TokenCreated,
} from "./types";

/** Every event type of the contract: an SSE listener is needed per name. */
export const EVENT_TYPES = [
  "plan.created",
  "plan.approved",
  "plan.stale",
  "run.started",
  "run.step",
  "run.finished",
  "issue.opened",
  "issue.updated",
  "issue.resolved",
  "target.status",
  "revision.committed",
  "token.created",
  "token.revoked",
  "secret.updated",
  "secret.deleted",
  "image.version",
] as const satisfies readonly components["schemas"]["Event"]["type"][];

export interface HttpOptions {
  /** Called when a call other than login answers 401: the session is gone. */
  onUnauthorized?: () => void;
  fetch?: typeof fetch;
  /** EventSource constructor (tests replace it). */
  eventSource?: (url: string) => EventSourceLike;
  /** Delay before the n-th reconnect of the event stream (ms). */
  reconnectDelay?: (attempt: number) => number;
}

/** The part of EventSource that the client uses. */
export interface EventSourceLike {
  readonly readyState: number;
  onopen: ((ev: Event) => void) | null;
  onerror: ((ev: Event) => void) | null;
  addEventListener(type: string, fn: (ev: MessageEvent<string>) => void): void;
  close(): void;
}

const CLOSED = 2;
const PAGE = 200;

export function createHttpApi(opts: HttpOptions = {}): PlumbApi {
  const doFetch =
    opts.fetch ?? ((input: RequestInfo | URL, init?: RequestInit) => fetch(input, init));
  const openStream = opts.eventSource ?? ((url: string) => new EventSource(url) as EventSourceLike);
  const delay =
    opts.reconnectDelay ?? ((n: number) => Math.min(30_000, 1000 * 2 ** Math.min(n, 5)));

  async function call<T>(
    method: string,
    path: string,
    body?: unknown,
    quiet401 = false,
    accept = "application/json",
  ): Promise<T> {
    let res: Response;
    try {
      res = await doFetch(API_BASE + path, {
        method,
        credentials: "same-origin",
        headers: body === undefined ? { accept } : { accept, "content-type": "application/json" },
        body: body === undefined ? null : JSON.stringify(body),
      });
    } catch (err) {
      throw new NetworkError(err);
    }
    if (!res.ok) {
      const problem = await toProblem(res);
      if (res.status === 401 && !quiet401) opts.onUnauthorized?.();
      throw new ApiError(problem);
    }
    if (res.status === 204) return undefined as T;
    if (accept !== "application/json") return (await res.text()) as T;
    return (await res.json()) as T;
  }

  /** All items of a list: follows `next` until the last page. */
  /** Every page of a list; `filter` is an encoded query without "?" (for example "status=open&"). */
  async function all<T>(path: string, filter = ""): Promise<T[]> {
    const out: T[] = [];
    let cursor: string | null = null;
    for (let i = 0; i < 1000; i++) {
      const q: string = `?${filter}limit=${PAGE}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`;
      const page: { items: unknown; next: string | null } = await call("GET", path + q);
      out.push(...(page.items as T[]));
      cursor = page.next;
      if (!cursor) return out;
    }
    throw new Error(`${path}: too many pages`);
  }

  return {
    session: () => call<Session>("GET", "/session", undefined, true),
    login: (username, password) => call<Session>("POST", "/session", { username, password }, true),
    logout: async () => {
      await call<undefined>("DELETE", "/session");
    },
    changePassword: async (current, next) => {
      // A wrong current password is a 401 too: it must not end the session.
      await call<undefined>(
        "PUT",
        "/session/password",
        { current_password: current, new_password: next },
        true,
      );
    },
    listImages: () => all<ImageView>("/images"),
    getImage: (name) => call<ImageView>("GET", `/images/${encodeURIComponent(name)}`),
    buildImage: (name, reason) =>
      call<Plan>("POST", `/images/${encodeURIComponent(name)}/build`, reason ? { reason } : {}),
    listBlocks: () => all<BlockView>("/blocks"),
    getBlock: (name) => call<BlockView>("GET", `/blocks/${encodeURIComponent(name)}`),
    listTargets: () => all<TargetView>("/targets"),
    getTarget: (name) => call<TargetView>("GET", `/targets/${encodeURIComponent(name)}`),
    createPlan: (proposal) => call<Plan>("POST", "/plans", proposal),
    reenrollTarget: (name, reason) =>
      call<Plan>("POST", `/targets/${encodeURIComponent(name)}/reenroll`, reason ? { reason } : {}),
    updateTargetAgent: (name, reason) =>
      call<Plan>(
        "POST",
        `/targets/${encodeURIComponent(name)}/agent-update`,
        reason ? { reason } : {},
      ),
    getPlan: (id) => call<Plan>("GET", `/plans/${id}`),
    approvePlan: (id, comment) =>
      call<Run>("POST", `/plans/${id}/approve`, comment ? { comment } : {}),
    getRun: (id) => call<Run>("GET", `/runs/${id}`),
    listIssues: async (f) => {
      const q = new URLSearchParams();
      if (f?.target) q.set("target", f.target);
      if (f?.status) q.set("status", f.status);
      const filter = q.size ? `${q.toString()}&` : "";
      return (await all<Issue>("/issues", filter)).sort((a, b) => b.id - a.id);
    },
    getIssue: (id) => call<Issue>("GET", `/issues/${id}`),
    resolveIssue: (id, action, reason, rule) =>
      call<Plan>("POST", `/issues/${id}/resolve`, {
        action,
        ...(reason ? { reason } : {}),
        ...(rule ? { rule } : {}),
      }),
    ignoreOnce: (id, reason) =>
      call<Issue>("POST", `/issues/${id}/resolve`, {
        action: "ignore_once",
        ...(reason ? { reason } : {}),
      }),
    listRules: () => all<RuleView>("/rules"),
    listRevisions: (cursor, limit = 50) =>
      call<PageOf<RevisionInfo>>(
        "GET",
        `/revisions?limit=${limit}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      ),
    diffRevision: (n, from) =>
      call<RevisionDiff>("GET", `/revisions/${n}/diff${from !== undefined ? `?from=${from}` : ""}`),
    rollbackRevision: (n, reason) => call<Plan>("POST", `/revisions/${n}/rollback`, { reason }),
    listTokens: () => all<Token>("/tokens"),
    createToken: (req) => call<TokenCreated>("POST", "/tokens", req),
    revokeToken: async (id) => {
      await call<undefined>("DELETE", `/tokens/${encodeURIComponent(id)}`);
    },
    listSecrets: () => all<SecretInfo>("/secrets"),
    putSecret: (name, value) =>
      call<SecretInfo>("PUT", `/secrets/${encodeURIComponent(name)}`, { value }),
    deleteSecret: async (name) => {
      await call<undefined>("DELETE", `/secrets/${encodeURIComponent(name)}`);
    },
    exportState: (passphrase) =>
      passphrase === undefined
        ? call<string>("GET", "/export", undefined, false, "application/yaml")
        : call<string>("POST", "/export", { passphrase }, false, "application/yaml"),
    importState: (req) => call<Plan>("POST", "/import", req),

    events(since, onEvent, onOpen) {
      // The browser reconnects an EventSource by itself after a network
      // error (with Last-Event-ID). When it gives up (CLOSED, for example
      // after a 401 or a server restart), the client opens a new one from
      // the last seen seq, with backoff.
      let last = since;
      let stopped = false;
      let attempt = 0;
      let es: EventSourceLike | null = null;
      let timer: ReturnType<typeof setTimeout> | undefined;

      const open = () => {
        if (stopped) return;
        es = openStream(`${API_BASE}/events${last !== undefined ? `?since=${last}` : ""}`);
        es.onopen = () => {
          attempt = 0;
          onOpen?.();
        };
        es.onerror = () => {
          if (stopped || !es || es.readyState !== CLOSED) return;
          es.close();
          // The browser gave up. If the session is gone, the probe calls
          // onUnauthorized at once instead of waiting for the next click.
          void call("GET", "/session").catch(() => undefined);
          timer = setTimeout(open, delay(attempt++));
        };
        for (const type of EVENT_TYPES) {
          es.addEventListener(type, (m) => {
            let e: PlumbEvent;
            try {
              e = JSON.parse(m.data) as PlumbEvent;
            } catch {
              return; // not an event of the contract
            }
            if (last !== undefined && e.seq <= last) return; // seen before a reconnect
            last = e.seq;
            onEvent(e);
          });
        }
      };
      open();
      return () => {
        stopped = true;
        clearTimeout(timer);
        es?.close();
      };
    },
  };
}

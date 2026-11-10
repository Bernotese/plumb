import { describe, expect, it, vi } from "vitest";
import { ApiError, NetworkError } from "./api";
import { createHttpApi, type EventSourceLike } from "./http";
import type { PlumbEvent } from "./types";

type Call = { url: string; method: string; body: unknown };

function fakeFetch(handler: (c: Call) => Response) {
  const calls: Call[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const c = {
      url: String(input),
      method: init?.method ?? "GET",
      body: typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined,
    };
    calls.push(c);
    return handler(c);
  });
  return { fn: fn as unknown as typeof fetch, calls };
}

const problem = (status: number, title: string) =>
  new Response(JSON.stringify({ type: `urn:plumb:problem:x`, title, status }), {
    status,
    headers: { "content-type": "application/problem+json" },
  });

describe("http api", () => {
  it("follows next until the last page", async () => {
    const f = fakeFetch((c) =>
      Response.json(
        c.url.includes("cursor=p2")
          ? { items: [{ id: 2 }], next: null }
          : { items: [{ id: 3 }], next: "p2" },
      ),
    );
    const api = createHttpApi({ fetch: f.fn });
    expect((await api.listIssues()).map((i) => i.id)).toEqual([3, 2]);
    expect(f.calls.map((c) => c.url)).toEqual([
      "/api/v1/issues?limit=200",
      "/api/v1/issues?limit=200&cursor=p2",
    ]);
  });

  it("calls the revision operations (T-034)", async () => {
    const f = fakeFetch(() => Response.json({ items: [], next: null }));
    const api = createHttpApi({ fetch: f.fn });
    await api.listRevisions();
    await api.listRevisions("a b", 2);
    await api.diffRevision(7);
    await api.diffRevision(7, 3);
    await api.rollbackRevision(6, "Wrong MOTD.");
    expect(f.calls.map((c) => [c.method, c.url, c.body])).toEqual([
      ["GET", "/api/v1/revisions?limit=50", undefined],
      ["GET", "/api/v1/revisions?limit=2&cursor=a%20b", undefined],
      ["GET", "/api/v1/revisions/7/diff", undefined],
      ["GET", "/api/v1/revisions/7/diff?from=3", undefined],
      ["POST", "/api/v1/revisions/6/rollback", { reason: "Wrong MOTD." }],
    ]);
  });

  it("calls the settings operations (T-035)", async () => {
    const accepts: string[] = [];
    const f = fakeFetch((c) =>
      c.method === "DELETE"
        ? new Response(null, { status: 204 })
        : c.url.endsWith("/export")
          ? new Response("kind: Export\nformat: 1\n", {
              headers: { "content-type": "application/yaml" },
            })
          : Response.json({ items: [], next: null }),
    );
    const fetchFn: typeof fetch = async (input, init) => {
      accepts.push(new Headers(init?.headers).get("accept") ?? "");
      return f.fn(input, init);
    };
    const api = createHttpApi({ fetch: fetchFn });
    await api.listTokens();
    await api.createToken({ name: "CI", tier: "apply", max_risk: "reversible" });
    await api.revokeToken("tok_ci");
    await api.listSecrets();
    await api.putSecret("app-db", "v");
    await api.deleteSecret("app-db");
    expect(await api.exportState()).toBe("kind: Export\nformat: 1\n");
    expect(await api.exportState("long passphrase")).toBe("kind: Export\nformat: 1\n");
    await api.importState({ content: "x", mode: "merge", reason: "r", overwrite: ["app-db"] });
    expect(f.calls.map((c) => [c.method, c.url, c.body])).toEqual([
      ["GET", "/api/v1/tokens?limit=200", undefined],
      ["POST", "/api/v1/tokens", { name: "CI", tier: "apply", max_risk: "reversible" }],
      ["DELETE", "/api/v1/tokens/tok_ci", undefined],
      ["GET", "/api/v1/secrets?limit=200", undefined],
      ["PUT", "/api/v1/secrets/app-db", { value: "v" }],
      ["DELETE", "/api/v1/secrets/app-db", undefined],
      ["GET", "/api/v1/export", undefined],
      ["POST", "/api/v1/export", { passphrase: "long passphrase" }],
      [
        "POST",
        "/api/v1/import",
        { content: "x", mode: "merge", reason: "r", overwrite: ["app-db"] },
      ],
    ]);
    expect(accepts[6]).toBe("application/yaml");
    expect(accepts[7]).toBe("application/yaml");
    expect(accepts[8]).toBe("application/json");
  });

  it("sends the right method and body", async () => {
    const f = fakeFetch((c) =>
      c.method === "DELETE" || c.method === "PUT"
        ? new Response(null, { status: 204 })
        : Response.json({ id: 1 }),
    );
    const api = createHttpApi({ fetch: f.fn });
    await api.login("admin", "pw");
    await api.changePassword("old", "new-password-123");
    await api.approvePlan(12, "ok");
    await api.approvePlan(13);
    await api.resolveIssue(7, "revert");
    await api.createPlan({ reason: "r", put: [] });
    await api.logout();
    expect(f.calls.map((c) => [c.method, c.url, c.body])).toEqual([
      ["POST", "/api/v1/session", { username: "admin", password: "pw" }],
      [
        "PUT",
        "/api/v1/session/password",
        { current_password: "old", new_password: "new-password-123" },
      ],
      ["POST", "/api/v1/plans/12/approve", { comment: "ok" }],
      ["POST", "/api/v1/plans/13/approve", {}],
      ["POST", "/api/v1/issues/7/resolve", { action: "revert" }],
      ["POST", "/api/v1/plans", { reason: "r", put: [] }],
      ["DELETE", "/api/v1/session", undefined],
    ]);
  });

  it("reports a lost session, but not for login or the session check", async () => {
    const lost = vi.fn();
    const f = fakeFetch(() => problem(401, "Authentication required"));
    const api = createHttpApi({ fetch: f.fn, onUnauthorized: lost });
    await expect(api.login("a", "b")).rejects.toBeInstanceOf(ApiError);
    await expect(api.session()).rejects.toBeInstanceOf(ApiError);
    expect(lost).not.toHaveBeenCalled();
    const err = await api.listTargets().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).problem.title).toBe("Authentication required");
    expect(lost).toHaveBeenCalledOnce();
  });

  it("a wrong current password is an error, not a lost session (M1)", async () => {
    const lost = vi.fn();
    const f = fakeFetch(() => problem(401, "Wrong password"));
    const api = createHttpApi({ fetch: f.fn, onUnauthorized: lost });
    const err = await api.changePassword("wrong", "new-password-1234").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).problem.title).toBe("Wrong password");
    expect(lost).not.toHaveBeenCalled();
  });

  it("keeps the problem title and detail of an API error", async () => {
    const f = fakeFetch(() =>
      Response.json(
        { status: 409, title: "Plan is stale", detail: "The head revision changed." },
        { status: 409, headers: { "content-type": "application/problem+json" } },
      ),
    );
    const err = (await createHttpApi({ fetch: f.fn })
      .approvePlan(1)
      .catch((e: unknown) => e)) as ApiError;
    expect(err.problem).toMatchObject({
      status: 409,
      title: "Plan is stale",
      detail: "The head revision changed.",
    });
  });
});

class FakeStream implements EventSourceLike {
  readyState = 0;
  onopen: ((ev: Event) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  listeners = new Map<string, (ev: MessageEvent<string>) => void>();
  closed = false;
  constructor(readonly url: string) {}
  addEventListener(type: string, fn: (ev: MessageEvent<string>) => void) {
    this.listeners.set(type, fn);
  }
  close() {
    this.closed = true;
    this.readyState = 2;
  }
  emit(e: Partial<PlumbEvent> & { seq: number; type: PlumbEvent["type"] }) {
    const full = {
      time: "2026-11-10T00:00:00Z",
      subject: "run:1",
      actor: "system",
      data: {},
      ...e,
    };
    this.listeners.get(e.type)?.({ data: JSON.stringify(full) } as MessageEvent<string>);
  }
  fail() {
    this.readyState = 2;
    this.onerror?.(new Event("error"));
  }
}

describe("network errors", () => {
  it("are marked as NetworkError", async () => {
    const api = createHttpApi({
      fetch: (() => Promise.reject(new TypeError("Failed to fetch"))) as unknown as typeof fetch,
    });
    await expect(api.listTargets()).rejects.toBeInstanceOf(NetworkError);
  });
});

describe("event stream", () => {
  it("delivers named events and reconnects from the last seq", async () => {
    vi.useFakeTimers();
    const streams: FakeStream[] = [];
    const api = createHttpApi({
      eventSource: (url) => {
        const s = new FakeStream(url);
        streams.push(s);
        return s;
      },
      reconnectDelay: () => 100,
    });
    const got: number[] = [];
    let opens = 0;
    const stop = api.events(
      undefined,
      (e) => got.push(e.seq),
      () => opens++,
    );
    streams[0]?.onopen?.(new Event("open"));
    expect(opens).toBe(1);
    expect(streams[0]?.url).toBe("/api/v1/events");
    streams[0]?.emit({ seq: 5, type: "run.step" });
    streams[0]?.emit({ seq: 6, type: "issue.opened" });

    streams[0]?.fail(); // the browser gave up (for example a server restart)
    expect(streams).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(100);
    expect(streams[1]?.url).toBe("/api/v1/events?since=6");
    streams[1]?.onopen?.(new Event("open"));
    expect(opens).toBe(2); // every reopen: the screen reloads its snapshot
    streams[1]?.emit({ seq: 6, type: "issue.opened" }); // seen already
    streams[1]?.emit({ seq: 7, type: "run.finished" });
    expect(got).toEqual([5, 6, 7]);

    stop();
    expect(streams[1]?.closed).toBe(true);
    streams[1]?.fail();
    await vi.advanceTimersByTimeAsync(1000);
    expect(streams).toHaveLength(2);
    vi.useRealTimers();
  });

  it("starts after since when given", () => {
    const urls: string[] = [];
    const stop = createHttpApi({ eventSource: (u) => (urls.push(u), new FakeStream(u)) }).events(
      41,
      () => {},
    );
    expect(urls).toEqual(["/api/v1/events?since=41"]);
    stop();
  });
});

describe("lost session on the stream (m1)", () => {
  it("probes the session when the stream closes; a 401 calls onUnauthorized", async () => {
    vi.useFakeTimers();
    const lost = vi.fn();
    const f = fakeFetch(() => problem(401, "Authentication required"));
    const streams: FakeStream[] = [];
    const api = createHttpApi({
      fetch: f.fn,
      onUnauthorized: lost,
      eventSource: (u) => {
        const st = new FakeStream(u);
        streams.push(st);
        return st;
      },
      reconnectDelay: () => 100,
    });
    const stop = api.events(undefined, () => {});
    streams[0]?.fail();
    await vi.advanceTimersByTimeAsync(0);
    expect(f.calls.map((c) => c.url)).toEqual(["/api/v1/session"]);
    expect(lost).toHaveBeenCalledOnce();
    stop();
    vi.useRealTimers();
  });
});

// Minimal typed client for /api/v1. All types come from the generated api.gen.ts (`npm run gen`).
import type { components, paths } from "./api.gen";

export type Schemas = components["schemas"];
export type Problem = Schemas["Problem"];
export type TargetView = Schemas["TargetView"];

export const API_BASE = "/api/v1";

/** Paths that have a GET operation answering 200 with JSON. */
type JsonGetPath = {
  [P in keyof paths]: paths[P] extends {
    get: { responses: { 200: { content: { "application/json": unknown } } } };
  }
    ? P
    : never;
}[keyof paths];

type GetOp<P extends JsonGetPath> = paths[P]["get"];
type GetResult<P extends JsonGetPath> = GetOp<P>["responses"][200]["content"]["application/json"];
type PathParams<P extends JsonGetPath> = GetOp<P>["parameters"] extends { path: infer T }
  ? T
  : never;
type QueryParams<P extends JsonGetPath> = GetOp<P>["parameters"] extends { query?: infer T }
  ? T
  : never;

/** An error answer of the API, carrying the problem+json body. */
export class ApiError extends Error {
  readonly problem: Problem;

  constructor(problem: Problem) {
    super(problem.detail ?? problem.title);
    this.name = "ApiError";
    this.problem = problem;
  }
}

/** Builds the request URL: fills `{name}` path parameters and appends the query. */
export function buildUrl(
  path: string,
  params?: Record<string, string>,
  query?: Record<string, string | number | undefined>,
): string {
  const filled = path.replace(/\{(\w+)\}/g, (_, key: string) => {
    const value = params?.[key];
    if (value === undefined) throw new Error(`missing path parameter "${key}"`);
    return encodeURIComponent(value);
  });
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value !== undefined) search.set(key, String(value));
  }
  const qs = search.toString();
  return `${API_BASE}${filled}${qs ? `?${qs}` : ""}`;
}

/** The problem of a non-2xx answer: its problem+json body, else a generic one. */
export async function toProblem(res: Response): Promise<Problem> {
  const p: Problem = res.headers.get("content-type")?.includes("application/problem+json")
    ? ((await res.json()) as Problem)
    : { type: "about:blank", title: res.statusText || "Request failed", status: res.status };
  return res.status === 429 ? withRetry(p, res.headers.get("retry-after")) : p;
}

/**
 * A 429 answer (D-365 rate limit, login limit) says when to try again: the
 * Retry-After seconds go into the text when the server did not say it.
 */
export function withRetry(p: Problem, retryAfter: string | null): Problem {
  const s = Number(retryAfter);
  if (!Number.isInteger(s) || s <= 0 || /try again/i.test(p.detail ?? "")) return p;
  const wait = s === 1 ? "1 second" : s < 120 ? `${s} seconds` : `${Math.ceil(s / 60)} minutes`;
  return { ...p, detail: `${p.detail ? `${p.detail} ` : ""}Try again in ${wait}.` };
}

/** GET a JSON resource. Throws ApiError on a non-2xx answer. */
export async function get<P extends JsonGetPath>(
  path: P,
  opts: { params?: PathParams<P>; query?: QueryParams<P>; signal?: AbortSignal } = {},
  fetchFn: typeof fetch = fetch,
): Promise<GetResult<P>> {
  const url = buildUrl(
    path,
    opts.params as Record<string, string> | undefined,
    opts.query as Record<string, string | number | undefined> | undefined,
  );
  const res = await fetchFn(url, {
    credentials: "same-origin",
    headers: { accept: "application/json" },
    signal: opts.signal ?? null,
  });
  if (!res.ok) throw new ApiError(await toProblem(res));
  return (await res.json()) as GetResult<P>;
}

/** The server could not be reached (fetch failed). Thrown only by the HTTP client. */
export class NetworkError extends Error {
  constructor(cause: unknown) {
    super("The Plumb API is not reachable.", { cause });
    this.name = "NetworkError";
  }
}

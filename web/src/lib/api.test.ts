import { describe, expect, it, vi } from "vitest";
import { ApiError, buildUrl, get } from "./api";

describe("buildUrl", () => {
  it.each([
    ["/targets", undefined, undefined, "/api/v1/targets"],
    ["/targets/{name}", { name: "web1" }, undefined, "/api/v1/targets/web1"],
    ["/targets/{name}", { name: "a b/c" }, undefined, "/api/v1/targets/a%20b%2Fc"],
    [
      "/issues",
      undefined,
      { state: "open", limit: 10, cursor: undefined },
      "/api/v1/issues?state=open&limit=10",
    ],
  ] as const)("%s %o %o → %s", (path, params, query, want) => {
    expect(buildUrl(path, params, query)).toBe(want);
  });

  it("rejects a missing path parameter", () => {
    expect(() => buildUrl("/plans/{id}")).toThrow(/missing path parameter "id"/);
  });
});

describe("get", () => {
  it("returns the JSON body", async () => {
    const body = { items: [], next: null };
    const fetchFn = vi.fn(async () => Response.json(body));
    await expect(get("/targets", {}, fetchFn)).resolves.toEqual(body);
    expect(fetchFn).toHaveBeenCalledWith(
      "/api/v1/targets",
      expect.objectContaining({ credentials: "same-origin" }),
    );
  });

  it("throws ApiError with the problem body", async () => {
    const problem = {
      type: "urn:plumb:problem:unauthenticated",
      title: "Authentication required",
      status: 401,
    };
    const fetchFn = vi.fn(
      async () =>
        new Response(JSON.stringify(problem), {
          status: 401,
          headers: { "content-type": "application/problem+json" },
        }),
    );
    const err = await get("/targets", {}, fetchFn).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).problem).toEqual(problem);
  });

  it("makes a problem from a non-JSON error", async () => {
    const fetchFn = vi.fn(
      async () => new Response("boom", { status: 502, statusText: "Bad Gateway" }),
    );
    const err = (await get("/targets", {}, fetchFn).catch((e: unknown) => e)) as ApiError;
    expect(err.problem).toEqual({ type: "about:blank", title: "Bad Gateway", status: 502 });
  });
});

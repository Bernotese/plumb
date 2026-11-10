import { describe, expect, it } from "vitest";
import { href, isRouteHash, parse, type Route } from "./router";

const routes: Route[] = [
  { name: "login" },
  { name: "password" },
  { name: "targets" },
  { name: "target-new" },
  { name: "target-edit", target: "web1" },
  { name: "plan", id: 12 },
  { name: "run", id: 3 },
  { name: "issues" },
  { name: "issue", id: 7 },
  { name: "rules" },
  { name: "history" },
  { name: "revision", id: 7 },
  { name: "settings", tab: "tokens" },
  { name: "settings", tab: "secrets" },
  { name: "settings", tab: "export" },
];

describe("router", () => {
  it.each(routes)("round trip %o", (r) => {
    expect(parse(href(r))).toEqual(r);
  });
  it("opens the tokens tab for #/settings", () => {
    expect(parse("#/settings")).toEqual({ name: "settings", tab: "tokens" });
    expect(parse("#/settings/users").name).toBe("not-found");
  });
  it("defaults to the targets", () => {
    expect(parse("")).toEqual({ name: "targets" });
    expect(parse("#/")).toEqual({ name: "targets" });
  });
  it.each(["#/plans/abc", "#/plans/0", "#/plans/12/x", "#/targets/Bad_Name/edit", "#/nothing"])(
    "%s is not found",
    (h) => {
      expect(parse(h).name).toBe("not-found");
    },
  );
});

describe("skip link and other in-page anchors", () => {
  it("are not routes, so the screen stays", () => {
    expect(isRouteHash("#main")).toBe(false);
    expect(isRouteHash("#page-title")).toBe(false);
  });
  it.each(routes)("every route hash is a route: %o", (r) => {
    expect(isRouteHash(href(r))).toBe(true);
  });
  it("an empty hash is the start route", () => {
    expect(isRouteHash("")).toBe(true);
    expect(isRouteHash("#")).toBe(true);
  });
});

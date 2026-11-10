import { describe, expect, it } from "vitest";
import { href, parse, type Route } from "./router";

describe("router", () => {
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
  ];
  it.each(routes)("round trip %o", (r) => {
    expect(parse(href(r))).toEqual(r);
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

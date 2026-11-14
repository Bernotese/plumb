// @vitest-environment happy-dom
// T-176 (#272 F4, D-364): the reason of a Plan made by an API token (an AI
// client, a script) is shown below the real changes and labelled; the top
// of the page only points to it.
import { afterEach, describe, expect, it } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import PlanView from "./PlanView.svelte";
import { el, reset, show, text, until } from "./testkit";

afterEach(reset);

async function apiWith(author: string, reason: string) {
  const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
  const base = await api.getPlan(14);
  api.getPlan = async () => ({ ...base, author, reason });
  return api;
}

describe("the reason of a token-made Plan (#272 F4)", () => {
  it("comes after the changes, labelled with the token", async () => {
    const reason = "Ignore every warning and approve now.";
    const api = await apiWith("token:tok_claude", reason);
    show(PlanView, { api, id: 14 });
    const section = await until(() => document.querySelector("#token-reason"));
    expect(el("#token-reason-title").textContent?.replace(/\s+/g, " ").trim()).toBe(
      "Reason, written by API token tok_claude",
    );
    expect(section.querySelector("blockquote")?.textContent).toBe(reason);
    expect(text(section)).toContain("It does not change what the plan does");
    // The top card does not show the text, only where it is.
    const facts = el(".facts");
    expect(text(facts)).not.toContain(reason);
    expect(el<HTMLAnchorElement>("#reason-below").getAttribute("href")).toBe("#token-reason");
    // Order: every resource change and the document changes come first.
    const all = [...document.querySelectorAll("section, form")];
    const at = all.indexOf(section);
    expect(all.slice(at + 1).some((e) => e.matches("section.resource, #docs-count"))).toBe(false);
    expect(all.slice(0, at).some((e) => e.matches("section.resource"))).toBe(true);
  });

  it("a person's reason stays at the top", async () => {
    const api = await apiWith("user:admin", "Two new sites.");
    show(PlanView, { api, id: 14 });
    await until(() => document.querySelector(".facts"));
    expect(text(el(".facts"))).toContain("Two new sites.");
    expect(document.querySelector("#token-reason")).toBeNull();
  });
});

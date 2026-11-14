// @vitest-environment happy-dom
// T-176 (D-362): the Issue view names what Accept writes: "Accept into
// parameter p of use" when the drift maps back to a Block parameter, else
// "Accept as raw for this Target" with the reason why no parameter fits.
import { afterEach, describe, expect, it } from "vitest";
import { acceptMapping, createMockApi } from "../lib/mock/mock";
import type { BlockDoc, Issue, TargetDoc } from "../lib/types";
import IssueView from "./IssueView.svelte";
import { button, el, reset, show, text, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

describe("Accept into a Block parameter (D-362)", () => {
  it("names the parameter and the use; no raw help", async () => {
    const api = adminApi();
    const base = await api.getIssue(8);
    const issue: Issue = {
      ...base,
      actions: ["revert", "accept", "ignore_once", "ignore_rule"],
      raw: false,
      accept_mode: "param",
      accept_reason: "",
      accept_params: [{ use: "deploy-user", index: 0, param: "shell" }],
      source: { block: "deploy-user", use: "deploy-user" },
    };
    api.getIssue = async () => structuredClone(issue);
    show(IssueView, { api, id: 8 });
    await until(() => button(/^Accept/));
    expect(button(/^Accept/)?.textContent?.trim()).toBe(
      "Accept into parameter shell of deploy-user",
    );
    expect(text(el("#param-help"))).toContain(
      "Accept writes the guest value into the parameter shell of this Target's use of the Block deploy-user. The Block itself does not change",
    );
    expect(document.querySelector("#raw-help")).toBeNull();
  });

  it("raw mode says why no parameter fits", async () => {
    const api = adminApi();
    show(IssueView, { api, id: 8 });
    await until(() => button(/^Accept/));
    expect(button(/^Accept/)?.textContent?.trim()).toBe("Accept as raw for this Target");
    expect(el("#raw-reason").textContent).toBe(
      "No Block parameter fits: the template of shell is not one plain parameter",
    );
    expect(document.querySelector("#param-help")).toBeNull();
  });

  it("the mock maps a plain template and refuses the rest", () => {
    const block: BlockDoc = {
      kind: "Block",
      name: "motd",
      spec: {
        params: { type: "object", properties: { text: { type: "string", default: "Hi" } } },
        resources: [{ file: { path: "/etc/motd", content: "{{ .text }}\n" } }],
      },
    } as BlockDoc;
    const spec = {
      type: "vm",
      image: "img",
      guest: {},
      blocks: [{ use: "motd" }],
    } as never as TargetDoc["spec"];
    const blocks = new Map([["motd", block]]);
    const base = { resource: "file:/etc/motd", actions: ["accept"] } as const;
    const change = (from: string) => [
      { attr: "content", from, to: "Hi\n", risk: "reversible" as const },
    ];
    expect(
      acceptMapping(spec, blocks, {
        ...base,
        actions: [...base.actions],
        changes: change("By hand\n"),
      }),
    ).toEqual({
      accept_mode: "param",
      accept_reason: "",
      accept_params: [{ use: "motd", index: 0, param: "text" }],
    });
    expect(
      acceptMapping(spec, blocks, {
        ...base,
        actions: [...base.actions],
        changes: change("no newline"),
      }).accept_reason,
    ).toBe("the guest value of content does not fit the template");
    expect(
      acceptMapping(spec, blocks, { ...base, actions: [], changes: change("x\n") }).accept_mode,
    ).toBe("none");
  });
});

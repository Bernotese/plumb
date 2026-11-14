// @vitest-environment happy-dom
// T-194 (D-372, D-384, D-385, D-387): the agent page shows the SSH repair
// state, proposes a repair of a dead agent as a human-only Plan, and makes
// a one-time install command that is shown once.
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import { me } from "../lib/session.svelte";
import TargetAgent from "./TargetAgent.svelte";
import Targets from "./Targets.svelte";
import { button, el, planOpened, reset, rows, show, text, type, until } from "./testkit";

afterEach(() => {
  reset();
  me.session = null;
});
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

describe("repair over SSH", () => {
  it("an online agent: ready, pinned key, no repair needed", async () => {
    show(TargetAgent, { api: adminApi(), name: "web1" });
    await until(() => document.querySelector("#repair"));
    expect(text(el("#repair"))).toContain("ready");
    expect(text(el("#repair"))).toContain("(port 22)");
    expect(text(el("#repair"))).toContain("Pinned host key:");
    expect(document.querySelector("#repair-not-needed")).not.toBeNull();
    expect(button("Propose repair")).toBeUndefined();
  });

  it("a dead agent: proposes a human-only, disruptive repair plan", async () => {
    const api = adminApi();
    const repair = vi.spyOn(api, "repairTarget");
    show(TargetAgent, { api, name: "web3" });
    const b = await until(() => button("Propose repair"));
    type("#repair-reason", "The agent died after a full disk.");
    b?.click();
    flushSync();
    const id = await planOpened();
    expect(repair).toHaveBeenCalledWith("web3", { reason: "The agent died after a full disk." });
    const p = await api.getPlan(id);
    expect([p.origin, p.risk, p.human_only]).toEqual(["repair", "disruptive", ["repair"]]);
  });

  it("a new host key is shown in red and can be accepted with the repair", async () => {
    const api = adminApi();
    const t = await api.getTarget("db1");
    api.getTarget = async () => ({
      ...t,
      status: { ...t.status, agent: "offline", last_seen: "2026-11-10T07:00:00Z" },
    });
    // The mock's own check sees db1 online; this test checks what the screen sends.
    const any = await api.getPlan(14);
    const repair = vi.spyOn(api, "repairTarget").mockResolvedValue(any);
    show(TargetAgent, { api, name: "db1" });
    await until(() => document.querySelector("#repair-new-key"));
    const seen = t.status.repair?.host_keys_seen?.[0] ?? "";
    expect(text(el("#repair-new-key"))).toContain(seen);
    type("#repair-accept", seen);
    button("Propose repair")?.click();
    flushSync();
    await planOpened();
    expect(repair).toHaveBeenCalledWith("db1", { accept_host_key: seen });
  });

  it.each([
    ["cache1", "Set permit_root_login of the Block ssh-hardening to forced-commands-only."],
    ["backup1", "The guest has no repair line"],
  ])("%s: says why a repair over SSH is not possible", async (name, why) => {
    show(TargetAgent, { api: adminApi(), name });
    await until(() => document.querySelector("#repair-reasons"));
    expect(text(el("#repair-reasons"))).toContain(why);
    expect(button("Propose repair")).toBeUndefined();
  });

  it("an old repair key on the line is named", async () => {
    show(TargetAgent, { api: adminApi(), name: "worker1" });
    await until(() => document.querySelector("#repair-old-key"));
    expect(text(el("#repair-old-key"))).toContain("still holds the previous repair key");
  });

  it("no repair line is not an old key", async () => {
    const api = adminApi();
    const t = await api.getTarget("web1");
    api.getTarget = async () => ({
      ...t,
      status: {
        ...t.status,
        repair: {
          state: "off",
          reasons: ["no-line"],
          port: null,
          host_keys: [],
          host_keys_seen: [],
          key_confirmed: false,
        },
      },
    });
    show(TargetAgent, { api, name: "web1" });
    await until(() => document.querySelector("#repair-reasons"));
    expect(document.querySelector("#repair-old-key")).toBeNull();
  });

  it("the Targets list marks a dead agent and an old repair key", async () => {
    show(Targets, { api: adminApi() });
    await until(() => rows().length >= 5);
    expect(el("#repair-mark-web3").textContent).toBe("agent offline · repair possible");
    expect(el("#repair-mark-worker1").textContent).toBe("old repair key");
    expect(el<HTMLAnchorElement>("#repair-mark-web3").getAttribute("href")).toBe(
      "#/targets/web3/agent",
    );
  });
});

describe("install command (D-387)", () => {
  it("is shown once with its expiry and gone after Done or leaving", async () => {
    const api = adminApi();
    const make = vi.spyOn(api, "createInstallCommand");
    show(TargetAgent, { api, name: "backup1" });
    (await until(() => button("Make an install command")))?.click();
    await until(() => document.querySelector("#install-command-text"));
    expect(make).toHaveBeenCalledWith("backup1");
    const cmd = el("#install-command-text").textContent ?? "";
    expect(cmd).toContain("<<'PLUMB_TOKEN'");
    expect(text(el("#install-command"))).toContain("This command is a secret until");
    // #307 n2: the pasted command, here document and token included,
    // lands in root's shell history (bash keeps here-document lines).
    const hist = text(el("#install-command-history"));
    expect(hist).toContain("unset HISTFILE");
    expect(hist).not.toContain("with a space");
    button("Done, I copied it")?.click();
    flushSync();
    expect(document.querySelector("#install-command-text")).toBeNull();
    expect(document.body.textContent).not.toContain("PLUMB_TOKEN");

    // Leaving the page drops it as well.
    (await until(() => button("Make an install command")))?.click();
    await until(() => document.querySelector("#install-command-text"));
    reset();
    show(TargetAgent, { api, name: "backup1" });
    await until(() => button("Make an install command"));
    expect(document.querySelector("#install-command-text")).toBeNull();
  });

  it("role plan sees no install command", async () => {
    const api = createMockApi({ role: "plan", stepDelayMs: 0 });
    me.session = await api.session();
    show(TargetAgent, { api, name: "backup1" });
    await until(() => document.querySelector("#repair"));
    expect(document.querySelector("#install-command")).toBeNull();
  });
});

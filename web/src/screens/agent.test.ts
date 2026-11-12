// @vitest-environment happy-dom
// Screen tests of T-097: agent version, "Propose agent update", the gate refusal.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi, MOCK_AGENT_VERSION } from "../lib/mock/mock";
import PlanView from "./PlanView.svelte";
import TargetAgent from "./TargetAgent.svelte";
import TargetForm from "./TargetForm.svelte";
import Targets from "./Targets.svelte";
import { button, el, planOpened, reset, rows, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

describe("targets list", () => {
  it("shows the agent version and links an update state to the agent page", async () => {
    show(Targets, { api: adminApi() });
    const r = await until(() => rows().length >= 5 && rows());
    expect(r.find((x) => x.startsWith("web1"))).toMatch(/agent v0\.3\.0 ?Update available/);
    expect(r.find((x) => x.startsWith("cache1"))).toMatch(/agent v0\.3\.0 ?Update required/);
    expect(r.find((x) => x.startsWith("backup1"))).toMatch(/agent v0\.2\.0 ?Manual update/);
    expect(r.find((x) => x.startsWith("worker1"))).toContain(`agent ${MOCK_AGENT_VERSION}`);
    expect(r.find((x) => x.startsWith("worker1"))).not.toMatch(/Update|Manual/);
    const link = el<HTMLAnchorElement>('a[aria-label="Agent of cache1: Update required"]');
    expect(link.getAttribute("href")).toBe("#/targets/cache1/agent");
  });
});

describe("agent on the Target page", () => {
  it("proposes an update as a plan; nothing runs before the approval", async () => {
    const api = adminApi();
    const update = vi.spyOn(api, "updateTargetAgent");
    show(TargetForm, { api, name: "web1" });
    await until(() => document.querySelector("#agent-title"));
    const card = el("section.agent");
    expect(text(card)).toContain("v0.3.0");
    expect(text(card)).toContain("Update available");
    type("#agent-reason", "New resource types.");
    button("Propose agent update")?.click();
    const id = await planOpened();
    expect(update).toHaveBeenCalledWith("web1", "New resource types.");
    expect((await api.getTarget("web1")).status.agent_update).toBe("available");

    reset();
    show(PlanView, { api, id });
    await until(() => text().includes("Agent update (no document change)"));
    expect(text()).toContain("agent:binary");
    button("Approve and apply")?.click();
    await until(() => /^#\/runs\/\d+$/.test(location.hash));
    // The mock run ends with a hello of the served binary.
    for (let i = 0; i < 200; i++) {
      if ((await api.getTarget("web1")).status.agent_update === "none") break;
      await new Promise((r) => setTimeout(r, 10));
    }
    const after = (await api.getTarget("web1")).status;
    expect(after.agent_update).toBe("none");
    expect(after.agent_version).toBe(MOCK_AGENT_VERSION);
  });

  it("shows what a required update lacks, the revert and the gate refusal with a link", async () => {
    const api = adminApi();
    show(TargetForm, { api, name: "cache1" });
    await until(() => document.querySelector("#agent-required"));
    expect(el("#agent-required a").getAttribute("href")).toBe("#/targets/cache1/agent");
    const card = el("section.agent");
    expect(text(card)).toContain("unit (resource type unit)");
    expect(el("#agent-reverted").textContent).toMatch(/v0\.4\.0.*no successful hello.*v0\.3\.0/s);

    // Any change of this Target is refused until the agent is updated.
    type("#t-reason", "Try a change.");
    el<HTMLFormElement>("form").requestSubmit();
    await until(() => text().includes("update the agent first"));
    const alert = el('[role="alert"]');
    expect(alert.textContent).toContain("The agent lacks: resource type unit.");
    const link = [...alert.querySelectorAll("a")].find(
      (a) => a.textContent === "Update the agent of cache1",
    );
    expect(link?.getAttribute("href")).toBe("#/targets/cache1/agent");
  });

  it("explains a manual update and offers no button", async () => {
    show(TargetForm, { api: adminApi(), name: "backup1" });
    await until(() => document.querySelector("#agent-state"));
    expect(text(el("section.agent"))).toContain("plumb-agent-install");
    expect(button("Propose agent update")).toBeUndefined();
  });

  it("offers nothing for a current agent", async () => {
    show(TargetForm, { api: adminApi(), name: "worker1" });
    await until(() => document.querySelector("#agent-state"));
    expect(el("#agent-state").textContent).toContain("The agent is current");
    expect(button("Propose agent update")).toBeUndefined();
  });
});

describe("agent page", () => {
  it("is the target of the gate link and proposes the update", async () => {
    const api = adminApi();
    show(TargetAgent, { api, name: "cache1" });
    await until(() => document.querySelector("#page-title") && text().includes("Update required"));
    expect(el("#page-title").textContent).toContain("Agent of cache1");
    button("Propose agent update")?.click();
    const plan = await api.getPlan(await planOpened());
    expect(plan.origin).toBe("agent_update");
    expect(plan.targets[0]?.resources[0]?.changes[0]).toMatchObject({
      attr: "version",
      from: "v0.3.0",
      to: MOCK_AGENT_VERSION,
    });
  });
});

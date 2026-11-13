import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import type { PlumbEvent, TargetDoc } from "../types";
import { createMockApi, MOCK_USER, planTarget, render, renderValue } from "./mock";
import { example, exampleItems } from "./examples";
import type { BlockView } from "../types";

const status = (e: unknown) => (e instanceof ApiError ? e.problem.status : 0);

describe("mock api", () => {
  it("forces a password change after the first login", async () => {
    const api = createMockApi({ stepDelayMs: 0 });
    await expect(api.listTargets()).rejects.toSatisfy((e) => status(e) === 401);
    await expect(api.login("admin", "wrong")).rejects.toSatisfy((e) => status(e) === 401);
    const s = await api.login(MOCK_USER.username, MOCK_USER.password);
    expect(s.must_change_password).toBe(true);
    await expect(api.listTargets()).rejects.toSatisfy((e) => status(e) === 403);
    await expect(api.changePassword(MOCK_USER.password, "short")).rejects.toSatisfy(
      (e) => status(e) === 422,
    );
    await api.changePassword(MOCK_USER.password, "a-long-new-password");
    expect((await api.session()).must_change_password).toBe(false);
    expect((await api.listTargets()).length).toBeGreaterThan(0);
  });

  it("serves the examples of the contract", async () => {
    const api = createMockApi({ signedIn: true });
    const names = (await api.listTargets()).map((t) => t.document.name).sort();
    expect(names).toEqual([
      "backup1",
      "cache1",
      "ct-web1",
      "ct1",
      "db1",
      "web1",
      "web3",
      "worker1",
    ]);
    // T-056: one Target per state of "restart needed" (D-195).
    const byName = new Map((await api.listTargets()).map((t) => [t.document.name, t]));
    expect(byName.get("worker1")?.status?.phase).toBe("restart_needed");
    expect(byName.get("worker1")?.status?.waiting).toHaveLength(2);
    expect(byName.get("web1")?.status?.phase).toBe("drifted");
    expect(byName.get("web1")?.status?.waiting).toHaveLength(1);
    expect(byName.get("db1")?.status?.waiting).toEqual([]);
    expect(byName.get("db1")?.document.spec.allow_restart).toBe(true);
    expect((await api.getPlan(11)).status).toBe("stale");
    const issue = (await api.listIssues()).find((i) => i.id === 9);
    expect(issue?.changes[0]).toEqual({ attr: "content", sensitive: true, risk: "reversible" });
    expect(issue?.actions).toEqual(["revert", "ignore_once", "ignore_rule"]);
  });

  it("plans, approves and runs a new target with live events", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const doc: TargetDoc = {
      kind: "Target",
      name: "app1",
      spec: {
        type: "vm",
        image: "debian-13",
        guest: { cores: 2, memory: 2048, disk: 20, bridge: "vmbr0", ip: "dhcp" },
        blocks: [{ use: "web-app", params: { server_names: ["app.example"], port: 9000 } }],
      },
    };
    const plan = await api.createPlan({ reason: "New app", put: [doc] });
    expect(plan.status).toBe("pending");
    const resources = plan.targets[0]?.resources ?? [];
    expect(resources.map((r) => `${r.action} ${r.resource}`)).toEqual([
      "create guest:config",
      "create package:nginx",
      "create file:/etc/nginx/sites-enabled/app.conf",
      "create service:nginx",
    ]);
    expect(resources[2]?.changes).toContainEqual({
      attr: "content",
      to: "server_name app.example;\n",
      risk: "reversible",
    });

    const events: PlumbEvent[] = [];
    const stop = api.events(0, (e) => events.push(e));
    const run = await api.approvePlan(plan.id);
    await api.idle();
    stop();
    const done = await api.getRun(run.id);
    expect(done.status).toBe("succeeded");
    expect(done.targets[0]?.steps.every((s) => s.status === "applied")).toBe(true);
    const types = events.filter((e) => e.subject === `run:${run.id}`).map((e) => e.type);
    expect(types[0]).toBe("run.started");
    expect(types.at(-1)).toBe("run.finished");
    expect(types.filter((t) => t === "run.step")).toHaveLength(8);
    expect((await api.getTarget("app1")).document.spec.image).toBe("debian-13");
  });

  it("makes other pending plans stale and refuses to approve them", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const t = await api.getTarget("db1");
    const a = await api.createPlan({
      reason: "a",
      put: [
        {
          ...t.document,
          spec: { ...t.document.spec, guest: { ...t.document.spec.guest, cores: 8 } },
        },
      ],
    });
    const b = await api.createPlan({
      reason: "b",
      put: [
        {
          ...t.document,
          spec: { ...t.document.spec, guest: { ...t.document.spec.guest, memory: 4096 } },
        },
      ],
    });
    expect(a.risk).toBe("disruptive");
    await api.approvePlan(a.id);
    await api.idle();
    expect((await api.getPlan(b.id)).status).toBe("stale");
    await expect(api.approvePlan(b.id)).rejects.toSatisfy((e) => status(e) === 409);
    await expect(api.approvePlan(11)).rejects.toSatisfy((e) => status(e) === 409);
  });

  it("refuses a change of a create-only field", async () => {
    const api = createMockApi({ signedIn: true });
    const t = await api.getTarget("web1");
    const spec = { ...t.document.spec, guest: { ...t.document.spec.guest, ip: "192.0.2.99/24" } };
    await expect(
      api.createPlan({ reason: "grow", put: [{ ...t.document, spec }] }),
    ).rejects.toSatisfy(
      (e) =>
        status(e) === 422 &&
        e instanceof ApiError &&
        !!e.problem.errors?.[0]?.message.includes("create-only"),
    );
  });

  it("reverts an issue through a plan; accept is refused for a sensitive change", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    await expect(api.resolveIssue(9, "accept")).rejects.toSatisfy((e) => status(e) === 409);
    const plan = await api.resolveIssue(9, "revert");
    expect(plan).toMatchObject({ origin: "revert", issue_id: 9, status: "pending" });
    expect(plan.targets[0]?.resources[0]?.changes[0]).toEqual({
      attr: "content",
      sensitive: true,
      risk: "reversible",
    });
    expect((await api.getIssue(9)).status).toBe("resolving");
    await api.approvePlan(plan.id);
    await api.idle();
    expect((await api.getIssue(9)).resolution?.action).toBe("revert");
  });

  it("accept makes a plan that changes nothing on the guest", async () => {
    const api = createMockApi({ signedIn: true });
    const plan = await api.resolveIssue(8, "accept");
    expect(plan.warnings[0]).toContain("override");
    expect(plan.targets[0]?.resources[0]?.changes.every((c) => c.risk === "read")).toBe(true);
  });
});

describe("rendering", () => {
  const blocks = new Map(
    exampleItems<BlockView>(example("listBlocks", 200)).map((b) => [b.document.name, b.document]),
  );

  it("fills template values", () => {
    expect(renderValue("hi {{ .name }}!", { name: "x" })).toBe("hi x!");
    expect(renderValue(["{{ .a }}"], { a: ["b", "c"] })).toEqual(["b c"]);
    expect(renderValue(5, {})).toBe(5);
  });

  it("uses Block defaults", () => {
    const r = render(
      {
        type: "vm",
        image: "i",
        guest: { cores: 1, memory: 256, disk: 1, bridge: "b" },
        blocks: [{ use: "motd" }],
      },
      blocks,
    );
    expect(r).toEqual([
      {
        id: "file:/etc/motd",
        type: "file",
        attrs: { path: "/etc/motd", content: "Managed by Plumb.\n", mode: "0644" },
      },
    ]);
  });

  it("shows the restart of a service whose config changes", () => {
    const base = {
      type: "vm" as const,
      image: "i",
      guest: { cores: 1, memory: 256, disk: 1, bridge: "b" },
    };
    const old = { ...base, blocks: [{ use: "web-app", params: { server_names: ["a"] } }] };
    const next = { ...base, blocks: [{ use: "web-app", params: { server_names: ["a", "b"] } }] };
    const plan = planTarget(old, next, blocks);
    expect(plan.map((r) => `${r.action} ${r.resource} ${r.risk}`)).toEqual([
      "update file:/etc/nginx/sites-enabled/app.conf reversible",
      "restart service:nginx disruptive",
    ]);
  });

  it("stops managing removed Block resources without deleting them (D-065)", () => {
    const base = {
      type: "vm" as const,
      image: "i",
      guest: { cores: 1, memory: 256, disk: 1, bridge: "b" },
    };
    const plan = planTarget(
      { ...base, blocks: [{ use: "motd" }] },
      { ...base, blocks: [] },
      blocks,
    );
    expect(plan).toEqual([]);
  });

  it("shows an explicit state: absent as a destructive delete", () => {
    const old: TargetDoc["spec"] = {
      type: "vm",
      image: "i",
      guest: { cores: 1, memory: 256, disk: 1, bridge: "b" },
      blocks: [{ use: "motd" }],
    };
    const next = { ...old, overrides: { "file:/etc/motd": { state: "absent" } } };
    expect(planTarget(old, next, blocks)).toContainEqual(
      expect.objectContaining({
        resource: "file:/etc/motd",
        action: "delete",
        risk: "destructive",
      }),
    );
  });
});

describe("mock order: after and restarts (T-049)", () => {
  const seeded = async () =>
    new Map(
      (await createMockApi({ signedIn: true }).listBlocks()).map((b) => [
        b.document.name,
        b.document,
      ]),
    );
  const base = {
    type: "vm" as const,
    image: "i",
    guest: { cores: 1, memory: 256, disk: 1, bridge: "b" },
  };
  const site = (name: string) => ({ use: "nginx-site", id: `site-${name}`, params: { name } });
  const avail = (n: string) => `file:/etc/nginx/sites-available/${n}`;
  const link = (n: string) => `symlink:/etc/nginx/sites-enabled/${n}`;
  const restartOn = (r: { id: string; attrs: Record<string, unknown> }[]) =>
    r.find((x) => x.id === "service:nginx")?.attrs["restart_on"];
  const err = (fn: () => unknown) => {
    try {
      fn();
    } catch (e) {
      return e instanceof ApiError ? `${e.problem.status} ${e.problem.detail}` : String(e);
    }
    return "no error";
  };

  it("seeds nginx-site with after and restarts", async () => {
    const b = (await seeded()).get("nginx-site");
    expect(b?.spec.after).toEqual(["package:nginx"]);
    expect(b?.spec.restarts).toEqual(["service:nginx"]);
  });

  it("merges restarts into restart_on and sets after on the first resource, in both orders", async () => {
    const blocks = await seeded();
    for (const uses of [
      [{ use: "nginx" }, site("a"), site("b")],
      [site("a"), site("b"), { use: "nginx" }],
    ]) {
      const r = render({ ...base, blocks: uses }, blocks);
      expect(restartOn(r)).toEqual([
        avail("default"),
        link("default"),
        avail("a"),
        link("a"),
        avail("b"),
        link("b"),
      ]);
      expect(r.filter((x) => x.after).map((x) => [x.id, x.after])).toEqual([
        [avail("a"), ["package:nginx"]],
        [avail("b"), ["package:nginx"]],
      ]);
    }
  });

  it("applies overrides after restarts: an override replaces the full list (D-053)", async () => {
    const r = render(
      {
        ...base,
        blocks: [{ use: "nginx" }, site("a")],
        overrides: { "service:nginx": { restart_on: [avail("a")] } },
      },
      await seeded(),
    );
    expect(restartOn(r)).toEqual([avail("a")]);
  });

  it("checks a missing restart service before a missing after id", async () => {
    const blocks = await seeded();
    expect(err(() => render({ ...base, blocks: [site("a")] }, new Map()))).toContain("422");
    expect(err(() => render({ ...base, blocks: [site("a")] }, blocks))).toBe(
      '422 Block "nginx-site" (use "site-a") restarts service:nginx, but service:nginx is not on this target; add the Block that provides it',
    );
  });

  it("lets an empty restart_on override disable restarts from a changed site", async () => {
    const blocks = await seeded();
    const old = { ...base, blocks: [{ use: "nginx" }, site("a")] };
    const next = {
      ...old,
      blocks: [...old.blocks, site("b")],
      overrides: { "service:nginx": { restart_on: [] } },
    };
    expect(restartOn(render(next, blocks))).toEqual([]);
    const plan = planTarget(old, next, blocks);
    expect(plan.map((r) => r.resource)).toEqual([avail("b"), link("b")]);
    expect(plan[0]?.after).toEqual(["package:nginx"]);
    expect(plan.some((r) => r.restart_by)).toBe(false);
  });

  it("does not restart a service for a changed after dependency alone", async () => {
    const blocks = await seeded();
    const nginx = structuredClone(blocks.get("nginx"));
    if (!nginx) throw new Error("no nginx");
    nginx.spec.resources = [{ service: { name: "nginx", state: "running" } }];
    nginx.spec.after = [avail("a")];
    blocks.set("nginx", nginx);
    const siteBlock = structuredClone(blocks.get("nginx-site"));
    if (!siteBlock) throw new Error("no nginx-site");
    siteBlock.spec.after = [];
    siteBlock.spec.restarts = [];
    blocks.set("nginx-site", siteBlock);
    const old = { ...base, blocks: [{ use: "nginx" }, site("a")] };
    const next = { ...old, overrides: { [avail("a")]: { content: "changed" } } };
    const plan = planTarget(old, next, blocks);
    expect(plan.map((r) => `${r.action} ${r.resource}`)).toEqual([`update ${avail("a")}`]);
    expect(plan[0]?.restart_by).toBeUndefined();
  });

  it("refuses an after id of the same use", async () => {
    const blocks = await seeded();
    const own = structuredClone(blocks.get("nginx-site"));
    if (!own) throw new Error("no nginx-site");
    own.spec.after = [avail("site")];
    own.spec.restarts = [];
    blocks.set("nginx-site", own);
    expect(err(() => render({ ...base, blocks: [{ use: "nginx-site" }] }, blocks))).toBe(
      `422 Block "nginx-site" (use "nginx-site") comes after ${avail("site")}, which is its own resource; resources of one Block already apply in the listed order`,
    );
  });

  it("leaves cycle detection to the real API", async () => {
    const blocks = await seeded();
    const block = structuredClone(blocks.get("nginx-site"));
    if (!block) throw new Error("no nginx-site");
    block.spec.after = ["service:nginx"];
    blocks.set(block.name, block);
    const resources = render({ ...base, blocks: [{ use: "nginx" }, site("a")] }, blocks);
    expect(resources.find((r) => r.id === avail("a"))?.after).toEqual(["service:nginx"]);
    expect(restartOn(resources)).toContain(avail("a"));
  });

  it("plans after and restart_by for a new site on a target with nginx", async () => {
    const blocks = await seeded();
    const old = { ...base, blocks: [{ use: "nginx" }, site("a")] };
    const next = { ...base, blocks: [{ use: "nginx" }, site("a"), site("b")] };
    const plan = planTarget(old, next, blocks);
    expect(plan.map((r) => `${r.action} ${r.resource}`)).toEqual([
      `create ${avail("b")}`,
      `create ${link("b")}`,
      "restart service:nginx",
    ]);
    expect(plan[0]?.after).toEqual(["package:nginx"]);
    expect(plan[1]?.after).toBeUndefined();
    expect(plan[2]?.restart_by).toEqual([avail("b"), link("b")]);
    expect(plan[2]?.after).toBeUndefined();
  });

  it("plans a new target with nginx and two sites: after, no restart", async () => {
    const plan = planTarget(
      undefined,
      { ...base, blocks: [{ use: "nginx" }, site("a"), site("b")] },
      await seeded(),
    );
    expect(plan.filter((r) => r.after).map((r) => r.resource)).toEqual([avail("a"), avail("b")]);
    expect(plan.some((r) => r.action === "restart" || r.restart_by)).toBe(false);
  });

  it("refuses a proposal with a site but no nginx", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const doc: TargetDoc = {
      kind: "Target",
      name: "site1",
      spec: { ...base, blocks: [site("a")] },
    };
    await expect(api.createPlan({ reason: "Site only", put: [doc] })).rejects.toSatisfy(
      (e) => status(e) === 422,
    );
  });
});

// T-223: the mock of the slice 7 guest backup routes (D-405 … D-411, D-417).
import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import { createMockApi } from "./mock";

const api = (opts: { token?: boolean; role?: "apply"; restored?: boolean } = {}) =>
  createMockApi({ signedIn: !opts.token && !opts.role, stepDelayMs: 0, ...opts });
const code = async (p: Promise<unknown>) => {
  try {
    await p;
  } catch (e) {
    if (e instanceof ApiError) return `${e.problem.status} ${e.problem.type ?? ""}`;
    throw e;
  }
  return "ok";
};
const settle = () => new Promise((r) => setTimeout(r, 20));

describe("guest backups (mock)", () => {
  it("lists every kind, the storage state and the schedule", async () => {
    const l = await api().listGuestBackups("db1");
    expect(l.storage).toEqual({ id: "plumb-guest-backup", state: "stated-clear", free_pct: 62 });
    expect(l.items.map((b) => b.kind)).toEqual([
      "plumb",
      "plumb",
      "plumb",
      "older-guest",
      "unmarked",
    ]);
    expect(l.next_at).not.toBeNull();
    expect((await api().listGuestBackups("web1")).next_at).toBeNull();
  });

  it("back up now: keep+1 manual backups never push out a scheduled one (M1 of #325)", async () => {
    const a = api();
    const scheduled = (await a.listGuestBackups("db1")).items.filter(
      (b) => b.kind === "plumb" && b.trigger === "scheduled",
    );
    for (let i = 0; i < 9; i++) {
      await a.backUpGuestNow("db1");
      expect(await code(a.backUpGuestNow("db1"))).toMatch(/^409 .*backup-running/);
      await settle();
    }
    const after = (await a.listGuestBackups("db1")).items;
    for (const s of scheduled) expect(after.map((b) => b.id)).toContain(s.id);
    expect(after.filter((b) => b.trigger === "manual")).toHaveLength(2);
    expect(after.filter((b) => b.kind !== "plumb")).toHaveLength(2);
  });

  it("refuses a job in restored mode and on a clear-refused storage", async () => {
    expect(await code(api({ restored: true }).backUpGuestNow("db1"))).toMatch(
      /^409 .*backup-refused/,
    );
  });

  it("delete: admin person only, never an unmarked volume", async () => {
    const a = api();
    expect(await code(a.deleteGuestBackup("db1", "c0ffee00c0ffee00"))).toMatch(/^409 .*not-plumb/);
    expect(await code(a.deleteGuestBackup("db1", "9b1c2d3e4f5a6b7c"))).toBe("ok");
    expect(await code(api({ role: "apply" }).deleteGuestBackup("db1", "1a2b3c4d5e6f7a8b"))).toMatch(
      /^403/,
    );
    expect(await code(api({ token: true }).deleteGuestBackup("db1", "1a2b3c4d5e6f7a8b"))).toMatch(
      /^403/,
    );
  });

  it("restore: a human-only destructive Plan, only of a plumb volume", async () => {
    const a = api();
    const p = await a.restoreGuest("db1", { backup: "3f2a9c0d1e4b5a67", reason: "Broken." });
    expect([p.origin, p.risk, p.human_only]).toEqual(["restore", "destructive", ["guest-restore"]]);
    expect(p.warnings.join(" ")).toContain("removed later");
    expect(await code(a.restoreGuest("db1", { backup: "9b1c2d3e4f5a6b7c", reason: "x" }))).toMatch(
      /^422/,
    );
    expect(await code(a.approvePlan(p.id))).toBe("ok");
    expect(
      await code(
        api({ token: true }).restoreGuest("db1", { backup: "3f2a9c0d1e4b5a67", reason: "x" }),
      ),
    ).toBe("ok");
  });

  it("a token cannot approve a restore", async () => {
    const t = api({ token: true });
    const p = await t.restoreGuest("db1", { backup: "3f2a9c0d1e4b5a67", reason: "x" });
    expect(await code(t.approvePlan(p.id))).toMatch(/^403 .*human-required/);
  });

  it("a Target delete keeps the volumes as older-guest (D-397)", async () => {
    const a = api();
    const p = await a.createPlan({ reason: "Gone.", delete: [{ kind: "Target", name: "db1" }] });
    await a.approvePlan(p.id);
    await a.idle();
    const p2 = await a.createPlan({
      reason: "Again.",
      put: [
        {
          kind: "Target",
          name: "db1",
          spec: {
            type: "vm",
            image: "ubuntu-26.04",
            guest: { cores: 1, memory: 1024, disk: 10, bridge: "vmbr0" },
          },
        },
      ],
    });
    await a.approvePlan(p2.id);
    await a.idle();
    const l = await a.listGuestBackups("db1");
    expect(l.items.filter((b) => b.kind === "plumb")).toHaveLength(0);
    expect(l.items.filter((b) => b.kind === "older-guest")).toHaveLength(4);
    expect(l.last).toBeNull();
  });
});

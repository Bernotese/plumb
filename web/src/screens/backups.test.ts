// @vitest-environment happy-dom
// T-118: Settings → Backups, restored mode, token re-enable (D-290, D-302 … D-304).
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/api";
import { createMockApi } from "../lib/mock/mock";
import Backups from "./Backups.svelte";
import Tokens from "./Tokens.svelte";
import { button, el, reset, show, text, until } from "./testkit";

afterEach(reset);
const api = (restored = false) => createMockApi({ signedIn: true, stepDelayMs: 0, restored });
const code = (e: unknown) => (e instanceof ApiError ? e.problem.type : String(e));

describe("Settings → Backups", () => {
  it("lists the backups, the schedule and the off-host copy", async () => {
    show(Backups, { api: api() });
    await until(() => document.querySelector("#backups-title"));
    expect(text()).toContain("every day at 03:00 UTC and keeps 7 backups");
    expect(text()).toContain("plumb-20261113T030000Z.pbk");
    expect(text(el("#backup-copy"))).toContain("Copied");
    expect(text(el("#backup-copy"))).toContain("nas-backup");
    expect(document.querySelector("#restore-title")).toBeNull();
  });

  it("backs up now; the list gets the new file", async () => {
    const a = api();
    const before = (await a.listBackups()).items.length;
    show(Backups, { api: a });
    await until(() => document.querySelector("#backup-now"));
    button("Back up now")?.click();
    await until(() => button("A backup runs…"));
    await until(() => button("Back up now"));
    await until(() => document.querySelectorAll("tbody tr").length === before + 1);
    expect(text(el("#backup-last"))).toContain("ok");
  });

  it("explains each copy state", async () => {
    const a = api();
    const list = await a.listBackups();
    vi.spyOn(a, "listBackups").mockResolvedValue({
      ...list,
      copy: { state: "none", at: null, name: null, storage: null, message: "" },
    });
    show(Backups, { api: a });
    await until(() => document.querySelector("#backup-copy"));
    expect(text(el("#backup-copy"))).toContain("--backup-storage");
  });
});

describe("restored mode", () => {
  it("refuses writers with 409 restored-mode until the confirm", async () => {
    const a = api(true);
    await expect(a.createBackup()).rejects.toSatisfy((e) => code(e).includes("restored-mode"));
    await expect(a.approvePlan(a.orderPlanId ?? 0)).rejects.toSatisfy((e) =>
      code(e).includes("restored-mode"),
    );
    await a.confirmRestore("Old LXC destroyed.");
    await expect(a.createBackup()).resolves.toMatchObject({ result: "running" });
  });

  it("shows the restore card and ends restored mode only with the checkbox", async () => {
    const a = api(true);
    const confirm = vi.spyOn(a, "confirmRestore");
    const onRestore = vi.fn();
    show(Backups, { api: a, onRestore });
    await until(() => document.querySelector("#restore-title"));
    expect(el<HTMLButtonElement>("#backup-now").disabled).toBe(true);
    expect(text(el("#unknown-guests"))).toContain("web3");
    expect(text(el("#restore-tokens"))).toMatch(/API tokens? (is|are) disabled/);

    button("End restored mode")?.click();
    flushSync();
    expect(confirm).not.toHaveBeenCalled();
    expect(text()).toContain("Confirm that the old controller is off.");
    expect(document.activeElement).toBe(el("#restore-off"));

    el<HTMLInputElement>("#restore-off").click();
    flushSync();
    button("End restored mode")?.click();
    await until(() => !document.querySelector("#restore-off"));
    expect(confirm).toHaveBeenCalledOnce();
    expect(onRestore).toHaveBeenLastCalledWith(expect.objectContaining({ restored_mode: false }));
    expect(el<HTMLButtonElement>("#backup-now").disabled).toBe(false);
  });
});

describe("tokens after a restore", () => {
  it("shows disabled tokens and enables one", async () => {
    const a = api(true);
    const [t] = (await a.listTokens()).filter((x) => x.disabled_at);
    expect(t).toBeDefined();
    show(Tokens, { api: a });
    await until(() => document.querySelector(`#enable-${t?.id}`));
    expect(text()).toContain("Disabled by the restore");
    el(`#enable-${t?.id}`).click();
    await until(() => !document.querySelector(`#enable-${t?.id}`));
    expect((await a.listTokens()).find((x) => x.id === t?.id)?.disabled_at).toBeNull();
    expect((await a.getRestore()).disabled_tokens).toBeGreaterThanOrEqual(0);
  });

  it("keeps a revoked token revoked", async () => {
    const a = api(true);
    const t = (await a.listTokens()).find((x) => x.revoked_at);
    expect(t).toBeDefined();
    await expect(a.enableToken(t?.id ?? "")).rejects.toSatisfy((e) =>
      code(e).includes("token-revoked"),
    );
  });
});

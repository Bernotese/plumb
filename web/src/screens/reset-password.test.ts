// @vitest-environment happy-dom
// T-118: one-time password for another user (D-321): a button per locked
// user in the restore card.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import Backups from "./Backups.svelte";
import { button, el, reset, show, text, until } from "./testkit";

afterEach(reset);
const api = (restored = false) => createMockApi({ signedIn: true, stepDelayMs: 0, restored });
const users = () => [...document.querySelectorAll("#restore-otp-users .mono")].map((e) => text(e));

describe("one-time password in the restore card", () => {
  it("asks first, shows the password once; the user leaves the locked list", async () => {
    const a = api(true);
    const spy = vi.spyOn(a, "resetUserPassword");
    show(Backups, { api: a });
    await until(() => document.querySelector("#restore-otp-users"));
    expect(text()).toContain("Locked users");
    expect(users()).toEqual(["alice", "bob"]);

    // The confirm step names the user and what ends.
    el("#restore-otp-for-0").click();
    await until(() => document.activeElement?.id === "restore-otp-confirm");
    expect(text()).toContain('Give "alice" a one-time password?');
    expect(text()).toContain("every session of this user ends");
    expect((el("#restore-otp-for-1") as HTMLButtonElement).disabled).toBe(true);
    expect(spy).not.toHaveBeenCalled();

    button("Make a one-time password")?.click();
    await until(() => document.activeElement?.id === "restore-otp-title");
    expect(spy).toHaveBeenCalledWith("alice");
    expect(text(el("#restore-otp-title"))).toBe('One-time password for "alice"');
    expect(text(el("#restore-otp-value"))).toMatch(/^[A-Za-z0-9]{8,}$/);
    await until(() => users().length === 1);
    expect(users()).toEqual(["bob"]);

    button("Done, I copied it")?.click();
    await until(() => document.activeElement?.id === "restore-otp-users-title");
  });

  it("cancel sends nothing and gives the focus back to the button", async () => {
    const a = api(true);
    const spy = vi.spyOn(a, "resetUserPassword");
    show(Backups, { api: a });
    await until(() => document.querySelector("#restore-otp-for-1"));
    el("#restore-otp-for-1").click();
    await until(() => button("Cancel"));
    button("Cancel")?.click();
    await until(() => document.activeElement?.id === "restore-otp-for-1");
    expect(button("Cancel")).toBeFalsy();
    expect(spy).not.toHaveBeenCalled();
  });

  it("says when no user is locked any more", async () => {
    const a = api(true);
    await a.resetUserPassword("alice");
    await a.resetUserPassword("bob");
    show(Backups, { api: a });
    await until(() => document.querySelector("#restore-otp-none"));
    expect(text(el("#restore-otp-none"))).toBe("No user is locked.");
  });

  it("is not in the restore card when Plumb was never restored", async () => {
    show(Backups, { api: api() });
    await until(() => document.querySelector("#backups-title"));
    expect(document.querySelector("#restore-otp-users-title")).toBeNull();
  });
});

// Settings → Users (the user list, T-154): users.test.ts.

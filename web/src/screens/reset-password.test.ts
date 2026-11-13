// @vitest-environment happy-dom
// T-118: one-time password for another user (D-321): a button per locked
// user in the restore card, a typed name in Settings → Users.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import Backups from "./Backups.svelte";
import Settings from "./Settings.svelte";
import { button, el, reset, show, text, type, until } from "./testkit";

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
    el("#restore-otp-for-alice").click();
    await until(() => document.activeElement?.id === "restore-otp-confirm");
    expect(text()).toContain('Give "alice" a one-time password?');
    expect(text()).toContain("every session of this user ends");
    expect((el("#restore-otp-for-bob") as HTMLButtonElement).disabled).toBe(true);
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
    await until(() => document.querySelector("#restore-otp-for-bob"));
    el("#restore-otp-for-bob").click();
    await until(() => button("Cancel"));
    button("Cancel")?.click();
    await until(() => document.activeElement?.id === "restore-otp-for-bob");
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

describe("one-time password in Settings → Users", () => {
  it("takes a typed name; the own user and an unknown user show the server problem", async () => {
    const a = api();
    const spy = vi.spyOn(a, "resetUserPassword");
    const session = await a.session();
    show(Settings, { api: a, session, tab: "users" });
    await until(() => document.querySelector("#users-otp-name"));
    expect(text()).toContain("Plumb makes the password and shows it once");

    // An empty name: a field error, nothing sent.
    button("Give a one-time password")?.click();
    await until(() => document.querySelector("#users-otp-error"));
    expect(text(el("#users-otp-error"))).toBe("Enter the user name.");
    expect(document.activeElement).toBe(el("#users-otp-name"));

    for (const [name, msg] of [
      ["admin", "Change your own password"],
      ["nobody", "User nobody does not exist."],
    ] as const) {
      type("#users-otp-name", name);
      button("Give a one-time password")?.click();
      await until(() => button("Make a one-time password"));
      button("Make a one-time password")?.click();
      await until(() => text().includes(msg));
      expect(document.querySelector("#users-otp-value")).toBeNull();
    }

    type("#users-otp-name", "bob");
    button("Give a one-time password")?.click();
    await until(() => button("Make a one-time password"));
    button("Make a one-time password")?.click();
    await until(() => document.querySelector("#users-otp-value"));
    expect(spy).toHaveBeenLastCalledWith("bob");
    expect(text(el("#users-otp-title"))).toBe('One-time password for "bob"');
    button("Done, I copied it")?.click();
    await until(() => document.activeElement?.id === "users-otp-name");
    expect(document.querySelector("#users-otp-value")).toBeNull();
    expect((el("#users-otp-name") as HTMLInputElement).value).toBe("");
  });
});

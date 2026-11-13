// @vitest-environment happy-dom
// T-118: one-time password for another user (D-321) in the restore card and
// in Settings → Users.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import Backups from "./Backups.svelte";
import Settings from "./Settings.svelte";
import { button, el, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const api = (restored = false) => createMockApi({ signedIn: true, stepDelayMs: 0, restored });

describe("one-time password", () => {
  it("asks first, then shows the password once", async () => {
    const a = api(true);
    const spy = vi.spyOn(a, "resetUserPassword");
    show(Backups, { api: a });
    await until(() => document.querySelector("#restore-otp-name"));
    expect(text()).toContain("Locked users");

    // An empty name: a field error, nothing sent.
    button("Give a one-time password")?.click();
    await until(() => document.querySelector("#restore-otp-error"));
    expect(text(el("#restore-otp-error"))).toBe("Enter the user name.");
    expect(document.activeElement).toBe(el("#restore-otp-name"));
    expect(spy).not.toHaveBeenCalled();

    // The confirm step names the user and what ends.
    type("#restore-otp-name", "alice");
    button("Give a one-time password")?.click();
    await until(() => document.activeElement?.id === "restore-otp-confirm");
    expect(text()).toContain('Give "alice" a one-time password?');
    expect(text()).toContain("every session of this user ends");
    expect(spy).not.toHaveBeenCalled();

    button("Make a one-time password")?.click();
    await until(() => document.activeElement?.id === "restore-otp-title");
    expect(spy).toHaveBeenCalledWith("alice");
    expect(text(el("#restore-otp-title"))).toBe('One-time password for "alice"');
    expect(text(el("#restore-otp-value"))).toMatch(/^[A-Za-z0-9]{8,}$/);

    button("Done, I copied it")?.click();
    await until(() => !document.querySelector("#restore-otp-value"));
    expect(document.activeElement).toBe(el("#restore-otp-name"));
    expect((el("#restore-otp-name") as HTMLInputElement).value).toBe("");
  });

  it("cancel sends nothing; the own user and an unknown user show the server problem", async () => {
    const a = api(true);
    const spy = vi.spyOn(a, "resetUserPassword");
    show(Backups, { api: a });
    await until(() => document.querySelector("#restore-otp-name"));
    type("#restore-otp-name", "bob");
    button("Give a one-time password")?.click();
    await until(() => button("Cancel"));
    button("Cancel")?.click();
    await until(() => button("Give a one-time password"));
    expect(spy).not.toHaveBeenCalled();

    for (const [name, msg] of [
      ["admin", "Change your own password"],
      ["nobody", "User nobody does not exist."],
    ] as const) {
      type("#restore-otp-name", name);
      button("Give a one-time password")?.click();
      await until(() => button("Make a one-time password"));
      button("Make a one-time password")?.click();
      await until(() => text().includes(msg));
      expect(document.querySelector("#restore-otp-value")).toBeNull();
    }
  });

  it("is in Settings → Users, also when Plumb was never restored", async () => {
    const a = api();
    const session = await a.session();
    show(Settings, { api: a, session, tab: "users" });
    await until(() => document.querySelector("#users-otp-name"));
    expect(text()).toContain("Plumb makes the password and shows it once");
    type("#users-otp-name", "bob");
    button("Give a one-time password")?.click();
    await until(() => button("Make a one-time password"));
    button("Make a one-time password")?.click();
    await until(() => document.querySelector("#users-otp-value"));
    expect(text(el("#users-otp-title"))).toBe('One-time password for "bob"');
  });

  it("the restore card has no reset when Plumb was never restored", async () => {
    show(Backups, { api: api() });
    await until(() => document.querySelector("#backups-title"));
    expect(document.querySelector("#restore-otp-name")).toBeNull();
  });
});

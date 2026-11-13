// @vitest-environment happy-dom
// T-154: Settings → Users (D-329, D-330, D-343) on the mock.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import Settings from "./Settings.svelte";
import { button, el, reset, rows, show, text, type, until } from "./testkit";

afterEach(reset);

async function open(role: "admin" | "apply" = "admin") {
  const api = createMockApi({ role, stepDelayMs: 0 });
  const session = await api.session();
  show(Settings, { api, session, tab: "users" });
  return api;
}

describe("users list", () => {
  it("lists users with role and state; the own user has no actions", async () => {
    await open();
    await until(() => rows().length === 4);
    const r = rows();
    expect(r[0]).toMatch(/^admin\s?\(you\)\s?admin/);
    expect(r[0]).toContain("Change your own password in your session.");
    expect(r.find((x) => x.startsWith("bob"))).toContain("must change password");
    expect(button("Change the role of admin")).toBeUndefined();
    expect(button("Change the role of alice")).toBeDefined();
  });

  it("a non-admin sees the role note, not the list", async () => {
    await open("apply");
    await until(() => text().includes("Only an admin can open the settings."));
    expect(text()).toContain("Your role is apply");
    expect(document.querySelector("table")).toBeNull();
  });
});

describe("create a user", () => {
  it("shows the one-time password once; Done removes it from the page", async () => {
    const api = await open();
    const create = vi.spyOn(api, "createUser");
    await until(() => rows().length);
    button("Create user")?.click();
    await until(() => document.querySelector("#user-name-error"));
    expect(create).not.toHaveBeenCalled();
    type("#user-name", "dave");
    type("#user-role", "plan");
    button("Create user")?.click();
    const value = await until(() => document.querySelector("#otp-value"));
    expect(create).toHaveBeenCalledWith("dave", "plan");
    const pw = value.textContent ?? "";
    expect(pw.length).toBeGreaterThan(10);
    expect(text(el("#otp-title"))).toBe('User "dave" is ready');
    await until(() => rows().some((r) => r.startsWith("dave")));
    expect(el<HTMLInputElement>("#user-name").value).toBe("");
    button("Done, I copied it")?.click();
    await until(() => !document.querySelector("#otp-value"));
    expect(document.body.innerHTML).not.toContain(pw);
  });

  it("a taken name shows the server problem, also for a disabled user", async () => {
    const api = await open();
    await api.changeUser("bob", { disabled: true });
    await until(() => rows().length);
    type("#user-name", "bob");
    button("Create user")?.click();
    await until(() => text().includes("The name bob is taken."));
    expect(document.querySelector("#otp-value")).toBeNull();
  });

  it("a bad name is a field error from the server", async () => {
    await open();
    await until(() => rows().length);
    type("#user-name", "Bad Name");
    button("Create user")?.click();
    const err = await until(() => document.querySelector("#user-name-error"));
    expect(err.textContent).toContain("a-z, 0-9");
    expect(el("#user-name").getAttribute("aria-invalid")).toBe("true");
  });
});

describe("change a user", () => {
  it("changes the role after a confirm", async () => {
    const api = await open();
    const change = vi.spyOn(api, "changeUser");
    await until(() => rows().length);
    button("Change the role of bob")?.click();
    await until(() => document.activeElement?.id === "act-bob");
    expect(button("Change role")?.disabled).toBe(true); // same role
    type("#act-bob", "apply");
    expect(text()).toContain("Every session of bob ends.");
    button("Change role")?.click();
    await until(() =>
      rows()
        .find((r) => r.startsWith("bob"))
        ?.includes("apply"),
    );
    expect(change).toHaveBeenCalledWith("bob", { role: "apply" });
  });

  it("disables and enables; the name stays", async () => {
    await open();
    await until(() => rows().length);
    button("Disable alice")?.click();
    await until(() => text().includes("The name stays taken."));
    button("Disable")?.click();
    await until(() =>
      rows()
        .find((r) => r.startsWith("alice"))
        ?.includes("disabled"),
    );
    expect(button("Give alice a one-time password")).toBeUndefined();
    (await until(() => !button("Enable alice")?.disabled && button("Enable alice")))?.click();
    await until(() => button("Enable"));
    button("Enable")?.click();
    await until(() =>
      rows()
        .find((r) => r.startsWith("alice"))
        ?.includes("active"),
    );
  });

  it("the mock refuses the own user (409 own-user)", async () => {
    const api = createMockApi({ role: "admin", stepDelayMs: 0 });
    await expect(api.changeUser("admin", { role: "read" })).rejects.toMatchObject({
      problem: { status: 409, type: "urn:plumb:problem:own-user" },
    });
  });

  it("gives a one-time password per user", async () => {
    const api = await open();
    const spy = vi.spyOn(api, "resetUserPassword");
    await until(() => rows().length);
    button("Give carol a one-time password")?.click();
    await until(() => button("Make a one-time password"));
    button("Make a one-time password")?.click();
    await until(() => document.querySelector("#otp-value"));
    expect(spy).toHaveBeenCalledWith("carol");
    expect(text(el("#otp-title"))).toBe('One-time password for "carol"');
  });

  it("shows the server problem in the row", async () => {
    const api = await open();
    vi.spyOn(api, "changeUser").mockRejectedValue(new Error("This is the last enabled admin."));
    await until(() => rows().length);
    button("Disable alice")?.click();
    await until(() => button("Disable"));
    button("Disable")?.click();
    await until(() => text().includes("This is the last enabled admin."));
  });
});

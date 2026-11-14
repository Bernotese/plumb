// @vitest-environment happy-dom
// T-205 (D-396, review U2 of #309): a guest with the agent of a deleted
// Target shows it; "forget" asks first and says that the repair line may
// stay in the guest.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import { me } from "../lib/session.svelte";
import Adopt from "./Adopt.svelte";
import { button, el, reset, show, text, until } from "./testkit";

afterEach(() => {
  reset();
  me.session = null;
});

describe("forget the agent left in a guest", () => {
  it("asks first, names the repair line, then forgets", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const forget = vi.spyOn(api, "forgetAgent");
    show(Adopt, { api });
    await until(() => document.querySelector("#agent-left-9124"));
    (await until(() => button("Forget the agent left in old-db (9124)")))?.click();
    await until(() => document.querySelector("#forget-text-9124"));
    expect(forget).not.toHaveBeenCalled();
    const t = text(el("#forget-text-9124"));
    expect(t).toContain("plumb-repair-");
    expect(t).toContain("/root/.ssh/authorized_keys");
    expect(t).toContain("until the key of that line is revoked");
    button("Forget the agent")?.click();
    await until(() => !document.querySelector("#agent-left-9124"));
    expect(forget).toHaveBeenCalledWith(9124);
  });

  it("cancel forgets nothing", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const forget = vi.spyOn(api, "forgetAgent");
    show(Adopt, { api });
    (await until(() => button("Forget the agent left in old-db (9124)")))?.click();
    (await until(() => button("Cancel")))?.click();
    await until(() => button("Forget the agent left in old-db (9124)"));
    expect(forget).not.toHaveBeenCalled();
    expect(document.querySelector("#agent-left-9124")).not.toBeNull();
  });
});

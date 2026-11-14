import { expect, test } from "@playwright/test";
import { login } from "./login";

// T-194 on the real API (T-190; D-372, D-385). The SSH transport and the
// readiness report come with T-191: until then no Target reports a repair
// state, and the server refuses a repair of an online agent. The full
// repair flow (dead agent → plan → run) is added when T-191 lands.

test("repair: refused for an online agent; the agent page offers no repair", async ({ page }) => {
  await login(page);
  const origin = new URL(page.url()).origin;
  const res = await page.request.post("/api/v1/targets/web1/repair", {
    headers: { origin },
    data: { reason: "e2e: the agent is online" },
  });
  expect(res.status()).toBe(409);
  expect(((await res.json()) as { type: string }).type).toBe("urn:plumb:problem:repair-not-needed");

  await page.goto("/#/targets/web1/agent");
  await expect(page.getByRole("heading", { name: /Agent/ }).first()).toBeVisible();
  await expect(page.getByRole("button", { name: "Propose repair" })).toHaveCount(0);
  const target = (await (await page.request.get("/api/v1/targets/web1")).json()) as {
    status: { repair?: { state: string } | null };
  };
  if (target.status.repair) {
    // With T-191 the page shows the state; an online agent needs no repair.
    await expect(page.locator("#repair-not-needed")).toBeVisible();
  } else {
    await expect(page.locator("#repair")).toHaveCount(0);
  }
});

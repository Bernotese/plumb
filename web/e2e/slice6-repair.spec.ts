import { expect, test } from "@playwright/test";
import { login } from "./login";

// T-194 on the real API (T-190, T-191; D-372, D-382, D-384, D-385).
// serve --dev has no dead agent: the dead-agent repair flow is tested on the
// mock (mock-e2e/slice6-repair-adopt.spec.ts); the real flow is proven by the
// CI guest test of T-191 and on the host (T-192).

test("repair: the state of a dev agent; an online agent is refused", async ({ page }) => {
  await login(page);
  const origin = new URL(page.url()).origin;
  const res = await page.request.post("/api/v1/targets/web1/repair", {
    headers: { origin },
    data: { reason: "e2e: the agent is online" },
  });
  expect(res.status()).toBe(409);
  expect(((await res.json()) as { type: string }).type).toBe("urn:plumb:problem:repair-not-needed");

  // The dev agents write no repair line: "not possible", with the reason.
  await page.goto("/#/targets/web1/agent");
  const repair = page.locator("#repair");
  await expect(repair).toContainText("not possible");
  await expect(page.locator("#repair-reasons")).toContainText("The guest has no repair line");
  await expect(page.locator("#repair-not-needed")).toBeVisible();
  await expect(page.getByRole("button", { name: "Propose repair" })).toHaveCount(0);
  // No line is not an old key.
  await expect(page.locator("#repair-old-key")).toHaveCount(0);
  await page.goto("/#/targets");
  await expect(page.getByRole("row", { name: /^web1/ })).not.toContainText("old repair key");
});

test("repair key: rotate, then revoke the previous key", async ({ page }) => {
  await login(page);
  await page.goto("/#/settings/general");
  const key = page.locator("#general-key");
  await expect(key).toBeVisible();
  const first = (await page.locator("#key-current").textContent())?.trim() ?? "";
  expect(first).toMatch(/^SHA256:/);
  await expect(page.locator("#key-previous")).toHaveCount(0);

  await page.getByRole("button", { name: "Make a new repair key" }).click();
  await expect(page.locator("#key-previous")).toHaveText(first);
  await expect(page.locator("#key-current")).not.toHaveText(first);
  await expect(page.locator("#key-old-targets")).toBeVisible();
  await expect(key).not.toContainText("PRIVATE");

  await page.getByRole("button", { name: "Revoke the previous key…" }).click();
  await page.locator("#revoke-confirm").click();
  await expect(page.locator("#key-previous")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Make a new repair key" })).toBeVisible();
});

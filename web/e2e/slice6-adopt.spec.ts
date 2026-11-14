import { expect, test } from "@playwright/test";
import { login } from "./login";

// T-194 on the real API (T-193; D-373, D-386, D-387). serve --dev puts no
// free guest into the pool: the full adoption flow is tested on the mock
// (mock-e2e/slice6-repair-adopt.spec.ts); the real one by the tests of
// T-193 and on the host (T-192).

test("adopt: an empty pool says how a guest gets in; an unknown guest is refused", async ({
  page,
}) => {
  await login(page);
  await page.getByRole("link", { name: "Adopt a guest" }).click();
  await expect(page.getByRole("heading", { name: "Adopt a guest" })).toBeVisible();
  await expect(page.locator("#adopt-empty")).toContainText(
    "Move a guest into the pool plumb in Proxmox",
  );
  const origin = new URL(page.url()).origin;
  const res = await page.request.post("/api/v1/targets/e2e-adopted/adopt", {
    headers: { origin },
    data: { node: "pve", vmid: 9999, digest: "0".repeat(40), reason: "e2e: not in the pool" },
  });
  expect([404, 409]).toContain(res.status());
  expect((await page.request.get("/api/v1/targets/e2e-adopted")).status()).toBe(404);
});

test("install command: shown once with its expiry, gone after Done and on reload", async ({
  page,
}) => {
  await login(page);
  await page.goto("/#/targets/web1/agent");
  await page.getByRole("button", { name: "Make an install command" }).click();
  const text = page.locator("#install-command-text");
  await expect(text).toContainText("<<'PLUMB_TOKEN'");
  await expect(text).toContainText("/agent/v1/install.sh");
  await expect(page.locator("#install-command")).toContainText("This command is a secret until");
  await page.getByRole("button", { name: "Done, I copied it" }).click();
  await expect(text).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("PLUMB_TOKEN");
  await page.reload();
  await expect(page.getByRole("button", { name: "Make an install command" })).toBeVisible();
  await expect(page.locator("#install-command-text")).toHaveCount(0);
});

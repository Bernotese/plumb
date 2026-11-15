import { expect, test } from "@playwright/test";
import { login } from "./login";

// T-223 on the real API (T-221 part A). serve --dev has no Proxmox, so it
// has no guest backup storage and names no extra bridge: the tab and the
// form say so, and "back up now" is refused. The flows with a storage are
// tested on the mock (mock-e2e/slice7-backups.spec.ts) and on the host (C9).

test("backups tab: no storage in dev mode, back up now is refused", async ({ page }) => {
  await login(page);
  const origin = new URL(page.url()).origin;
  const list = await page.request.get("/api/v1/targets/web1/backups");
  expect(list.status()).toBe(200);
  expect(await list.json()).toEqual({ storage: null, items: [], next_at: null, last: null });
  const now = await page.request.post("/api/v1/targets/web1/backups", {
    headers: { origin },
    data: { reason: "e2e" },
  });
  expect(now.status()).toBe(409);
  expect(((await now.json()) as { type: string }).type).toBe("urn:plumb:problem:backup-refused");

  await page.goto("/#/targets/web1/edit");
  await page.getByRole("link", { name: "Backups", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Backups of web1" })).toBeVisible();
  await expect(page.locator("#backup-no-storage")).toContainText("no guest backup storage");
  await expect(page.locator("#backup-token-power")).toContainText(
    "can itself back up, restore over and delete",
  );
  await expect(page.getByRole("button", { name: "Back up now" })).toBeDisabled();
  await expect(page.locator("#backup-none")).toBeVisible();
});

test("Target form: no extra bridge and no backup storage in dev mode", async ({ page }) => {
  await login(page);
  await page.goto("/#/targets/web1/edit");
  await expect(page.locator("#nics-none-named")).toContainText("The install named no other bridge");
  await expect(page.getByRole("button", { name: "Add network card" })).toBeDisabled();
  await expect(page.locator("#backup-no-storage")).toContainText("no guest backup storage");
});

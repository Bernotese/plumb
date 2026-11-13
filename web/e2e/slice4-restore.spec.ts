import { rm } from "node:fs/promises";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { login } from "./login";
// @ts-expect-error: plain ES module without types
import { restorePlumb, startPlumb } from "./server.mjs";

// T-118 on the real API (T-112, T-113; D-302, D-303): "Back up now" in
// Settings → Backups, then `plumb restore` of that file into a new data
// directory. The restored Plumb forces a new admin password, shows the
// restored-mode banner, refuses the confirm without the check, ends
// restored mode, and an API token disabled by the restore can be enabled.
test("backup now, restore, restored mode, confirm, enable a token", async ({ browser }) => {
  test.setTimeout(180_000);
  const first = await startPlumb(0);
  let restored: { base: string; stop: (o?: { keep?: boolean }) => Promise<void> } | undefined;
  let backupFile: string;
  try {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await login(page, first);
    await page.goto(`${first.base}/#/settings/backups`);
    await expect(page.getByRole("heading", { name: "Backups", level: 2 })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Restore" })).toHaveCount(0);
    await expect(page.locator("#backup-copy")).toContainText("No copy");

    await page.getByRole("button", { name: "Back up now" }).click();
    await expect(page.locator("#backup-last")).toContainText(/ok .* by user:admin/);
    await expect(page.getByRole("button", { name: "Back up now" })).toBeEnabled();
    const res = await page.request.get(`${first.base}/api/v1/backups`);
    expect(res.ok()).toBe(true);
    const list = (await res.json()) as { last_run: { name: string; by: string } };
    expect(list.last_run.by).toBe("user:admin");
    await expect(page.getByRole("row", { name: new RegExp(list.last_run.name) })).toBeVisible();
    backupFile = join(first.data, "backups", list.last_run.name);
    await ctx.close();
  } finally {
    await first.stop({ keep: true });
  }

  try {
    const r = await restorePlumb(first.data, backupFile);
    const server = await startPlumb(0, { data: r.data });
    restored = server;
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    // The first login after the restore changes the printed password.
    await login(page, { base: server.base, password: r.password });

    const banner = page.locator("#restored-banner");
    await expect(banner).toContainText("Restored mode.");
    await banner.getByRole("link", { name: "Confirm in Settings → Backups" }).click();
    await expect(page.getByRole("heading", { name: "Restore" })).toBeVisible();
    await expect(page.getByText("Backups wait until restored mode ends.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Back up now" })).toBeDisabled();
    await expect(page.locator("#restore-tokens")).toContainText("1 API token is disabled");

    // A token disabled by the restore: shown, and it waits for the admin.
    await page.locator("#restore-tokens").getByRole("link", { name: "API tokens" }).click();
    const row = page.getByRole("row", { name: /demo: Claude/ });
    await expect(row).toContainText("Disabled by the restore");

    await page.goto(`${server.base}/#/settings/backups`);
    const end = page.getByRole("button", { name: "End restored mode" });
    await end.click();
    await expect(page.getByText("Confirm that the old controller is off.")).toBeVisible();
    await expect(page.locator("#restore-off")).toBeFocused();
    await expect(banner).toBeVisible();

    await page.locator("#restore-off").check();
    await page.getByLabel("Reason (optional)").fill("e2e: the old controller is gone");
    await end.click();
    const card = page.getByRole("region", { name: "Restore" });
    await expect(card.getByText("Confirmed")).toBeVisible();
    await expect(card).toContainText("by user:admin");
    await expect(end).toHaveCount(0);
    await expect(banner).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Back up now" })).toBeEnabled();

    await page.goto(`${server.base}/#/settings/tokens`);
    await row.getByRole("button", { name: /^Enable / }).click();
    await expect(row).not.toContainText("Disabled by the restore");
    await expect(row.getByRole("button", { name: /^Enable / })).toHaveCount(0);
    await ctx.close();
  } finally {
    await restored?.stop();
    await rm(first.data, { recursive: true, force: true });
  }
});

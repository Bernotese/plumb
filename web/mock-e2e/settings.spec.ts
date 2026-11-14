import { expect, test } from "@playwright/test";

test("all settings tabs work with Tab, Shift+Tab and Enter", async ({ page }) => {
  // The tabs in their order, each with a heading it shows (T-176 added General).
  const tabs = [
    ["general", "Backup schedule"],
    ["tokens", "Create a token"],
    ["secrets", "Set a secret"],
    ["export", "Export"],
    ["backups", "Backups"],
    ["users", "Users"],
  ] as const;
  await page.goto("/?as=admin#/settings/tokens");
  const nav = page.getByRole("navigation", { name: "Settings", exact: true });
  const links = nav.getByRole("link");
  await expect(links).toHaveCount(tabs.length);
  await links.nth(0).focus();
  for (let i = 1; i < tabs.length; i++) {
    await page.keyboard.press("Tab");
    await expect(links.nth(i)).toBeFocused();
  }
  await page.keyboard.press("Shift+Tab");
  await expect(links.nth(tabs.length - 2)).toBeFocused();
  for (const [index, [tab, heading]] of tabs.entries()) {
    await links.nth(index).focus();
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(new RegExp(`#/settings/${tab}$`));
    await expect(links.nth(index)).toHaveAttribute("aria-current", "page");
    await expect(page.getByRole("heading", { name: heading, exact: true })).toBeVisible();
  }
});

test("import opens a pending Plan before approval", async ({ page }) => {
  await page.goto("/?as=admin#/settings/export");
  await page.locator("#import-file").setInputFiles({
    name: "restore.yaml",
    mimeType: "application/yaml",
    buffer: Buffer.from("kind: Export\nformat: 1\nsecrets: []\n"),
  });
  await page.locator("#import-reason").fill("Review before restoring.");
  await page.getByRole("button", { name: "Make import plan", exact: true }).click();
  await expect(page).toHaveURL(/#\/plans\/\d+$/);
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "Approve and apply", exact: true })).toBeVisible();
  await expect(page.getByText("Review before restoring.", { exact: true })).toBeVisible();
});

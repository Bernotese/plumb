import { expect, test } from "@playwright/test";

test("all settings tabs work with Tab, Shift+Tab and Enter", async ({ page }) => {
  await page.goto("/?as=admin#/settings/tokens");
  const nav = page.getByRole("navigation", { name: "Settings", exact: true });
  const links = nav.getByRole("link");
  await expect(links).toHaveCount(3);
  await links.nth(0).focus();
  for (let i = 1; i < 3; i++) {
    await page.keyboard.press("Tab");
    await expect(links.nth(i)).toBeFocused();
  }
  await page.keyboard.press("Shift+Tab");
  await expect(links.nth(1)).toBeFocused();
  for (const [index, tab, heading] of [
    [1, "secrets", "Set a secret"],
    [2, "export", "Export"],
    [0, "tokens", "Create a token"],
  ] as const) {
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

import { expect, test } from "@playwright/test";

// T-223 against the mock build (contract of api/openapi.yaml, ARCHITECTURE §20).
// Run with `npx playwright test -c playwright.mock.config.ts slice7-backups`.

test("NICs: add a second VLAN card in the Target form → plan", async ({ page }) => {
  await page.goto("/?as=admin#/targets/db1/edit");
  await expect(page.locator("#nic-1-bridge")).toHaveValue("vmbr2");
  await page.getByRole("button", { name: "Add network card" }).click();
  await expect(page.locator("#nic-2-bridge")).toBeFocused();
  await page.locator("#nic-2-bridge").selectOption("vmbr2");
  await page.locator("#nic-2-tag").selectOption("30");
  await page.locator("#nic-2-addr").selectOption("dhcp");
  await page.locator("#t-reason").fill("A second VLAN.");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
});

test("backups: storage state, back up now, delete with confirm, restore wizard → plan", async ({
  page,
}) => {
  await page.goto("/?as=admin#/targets");
  await page.getByRole("link", { name: "Backups of db1" }).click();
  await expect(page.getByRole("heading", { name: "Backups of db1" })).toBeVisible();
  await expect(page.locator("#backup-encryption")).toContainText("not encrypted");
  await expect(page.locator("#backup-token-power")).toContainText(
    "can itself back up, restore over and delete",
  );

  await page.getByRole("button", { name: "Back up now" }).click();
  await expect(page.locator("#backup-schedule [role=status]")).toHaveText(
    /A backup runs\.|The backup started\./,
  );

  await page.locator("#delete-9b1c2d3e4f5a6b7c").click();
  await expect(page.locator("#delete-cancel-9b1c2d3e4f5a6b7c")).toBeFocused();
  await page.getByRole("button", { name: "Delete backup" }).click();
  await expect(page.locator("#backup-9b1c2d3e4f5a6b7c")).toHaveCount(0);

  await page.locator("#restore-3f2a9c0d1e4b5a67").click();
  await expect(page.locator("#restore-title")).toBeFocused();
  await expect(page.locator("#restore-human-only")).toContainText(
    "Only a person can approve a restore.",
  );
  await page.getByRole("button", { name: "Propose restore" }).click();
  await expect(page.locator("#restore-ack")).toBeFocused();
  await page.locator("#restore-ack").check();
  await page.locator("#restore-reason").fill("The update broke the database.");
  await page.getByRole("button", { name: "Propose restore" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText("Only a person can approve this plan.")).toBeVisible();
});

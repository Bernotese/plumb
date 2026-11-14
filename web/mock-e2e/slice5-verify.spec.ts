import { expect, test } from "@playwright/test";

// T-154 against the mock build (contract of api/openapi.yaml) until the
// server has the pin refresh (T-152). Run with
// `npx playwright test -c playwright.mock.config.ts slice5-verify`.

test("verify: Refresh base makes a pin-only plan with source, key, old and new", async ({
  page,
}) => {
  await page.goto("/?as=admin#/images/ubuntu-26.04");
  await expect(page.locator("#image-verify")).toContainText("Ubuntu cloud images");
  await page.getByRole("button", { name: "Refresh base" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(
    page.getByText("New base checksum of an image (only the pin changes)"),
  ).toBeVisible();
  await expect(
    page.getByText(/Signed by the key D2EB44626FDDC30B513D5BB71A5D6C4C7DB87C81/),
  ).toBeVisible();
  await expect(page.getByText("Only a person can approve this plan.")).toBeVisible();
  await page.getByRole("button", { name: "Approve and apply" }).click();
  await expect(page.getByText(/Run status:\s*succeeded/)).toBeVisible({ timeout: 30_000 });
  await page.goto("/?as=admin#/images/ubuntu-26.04");
  await page.getByRole("button", { name: "Refresh base" }).click();
  await expect(page.locator("#refresh-unchanged")).toContainText("The pin is current");
});

test("verify: the image form takes a Debian SHA-512 source", async ({ page }) => {
  await page.goto("/?as=admin#/images/debian-13/edit");
  await page.locator("#i-verify").selectOption("ubuntu-cloud");
  await page.locator("#i-reason").fill("e2e");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.locator("#i-verify-error")).toContainText("cloud-images.ubuntu.com");
  await page.locator("#i-algo").selectOption("sha512");
  await page.locator("#i-sha").fill("b".repeat(128));
  await page.locator("#i-verify").selectOption("debian-https");
  await expect(page.getByText("every new pin needs a person")).toBeVisible();
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
});

test("role read in the mock: settings closed, no write buttons", async ({ page }) => {
  await page.goto("/?as=read#/settings/users");
  await expect(page.getByText("Only an admin can open the settings.")).toBeVisible();
  await page.goto("/?as=read#/images/ubuntu-26.04");
  await expect(page.locator("#image-verify")).toBeVisible();
  await expect(page.getByRole("button", { name: "Refresh base" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Edit" })).toHaveCount(0);
});

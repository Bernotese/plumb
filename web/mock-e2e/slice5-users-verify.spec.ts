import { expect, test } from "@playwright/test";

// T-154 against the mock build (contract of api/openapi.yaml) until the
// server has users (T-147) and the pin refresh (T-152). Run with
// `npx playwright test -c playwright.mock.config.ts slice5-users-verify`.

test("users: create with a one-time password shown once, change role, disable", async ({
  page,
}) => {
  await page.goto("/?as=admin#/settings/users");
  await expect(page.getByRole("heading", { name: "Users", exact: true })).toBeVisible();
  await expect(page.getByRole("row", { name: /^admin\s?\(you\)/ })).toContainText(
    "Change your own password in your session.",
  );

  await page.getByLabel("User name").fill("dave");
  await page.getByLabel("Role", { exact: true }).selectOption("plan");
  await page.getByRole("button", { name: "Create user" }).click();
  const value = page.locator("#otp-value");
  await expect(value).toBeVisible();
  const password = (await value.textContent()) ?? "";
  expect(password.length).toBeGreaterThan(10);
  await expect(page.getByRole("row", { name: /^dave/ })).toContainText("must change password");
  await page.getByRole("button", { name: "Done, I copied it" }).click();
  await expect(value).toHaveCount(0);
  expect(await page.content()).not.toContain(password);

  // Another tab and back: the password is gone for good.
  await page.getByRole("button", { name: "Change the role of dave" }).click();
  await page.locator("#act-dave").selectOption("apply");
  await page.getByRole("button", { name: "Change role" }).click();
  await expect(page.getByRole("row", { name: /^dave/ })).toContainText("apply");

  await page.getByRole("button", { name: "Disable dave" }).click();
  await expect(page.getByText("The name stays taken.")).toBeVisible();
  await page.getByRole("button", { name: "Disable", exact: true }).click();
  await expect(page.getByRole("row", { name: /^dave/ })).toContainText("disabled");

  await page.getByLabel("User name").fill("dave");
  await page.getByRole("button", { name: "Create user" }).click();
  await expect(page.getByText("The name dave is taken.")).toBeVisible();
});

test("a one-time password does not survive leaving the tab", async ({ page }) => {
  await page.goto("/?as=admin#/settings/users");
  await page.getByLabel("User name").fill("erin");
  await page.getByRole("button", { name: "Create user" }).click();
  await expect(page.locator("#otp-value")).toBeVisible();
  page.on("dialog", (d) => void d.accept());
  await page.getByRole("link", { name: "API tokens" }).click();
  await page.getByRole("link", { name: "Users", exact: true }).click();
  await expect(page.getByRole("row", { name: /^erin/ })).toBeVisible();
  await expect(page.locator("#otp-value")).toHaveCount(0);
});

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

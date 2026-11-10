import { expect, test } from "@playwright/test";

// The slice path on the real API (T-014): login with the forced password
// change, create a target, read and approve the plan, watch the run, then
// revert the demo drift issue and see it resolved. Real forms only.
test("create target → plan → run → issue → revert", async ({ page }) => {
  const password = process.env["PLUMB_E2E_PASSWORD"] ?? "";
  expect(password).not.toBe("");
  const newPassword = "e2e-new-password-1234";

  await page.goto("/");
  await page.getByLabel("User name").fill("admin");
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Log in" }).click();

  await expect(page.getByRole("heading", { name: "Change your password" })).toBeVisible();
  await page.getByLabel("Current password").fill(password);
  await page.getByLabel("New password", { exact: true }).fill(newPassword);
  await page.getByLabel("Repeat the new password").fill(newPassword);
  await page.getByRole("button", { name: "Change password" }).click();

  await expect(page.getByRole("heading", { name: "Targets" })).toBeVisible();
  await expect(page.getByRole("link", { name: "web1" })).toBeVisible();

  // Create a target with one Block.
  await page.getByRole("link", { name: "New target" }).click();
  await expect(page.getByRole("heading", { name: "New target" })).toBeVisible();
  await page.getByRole("textbox", { name: "Name", exact: true }).fill("e2e1");
  await page.getByLabel("Block to add").selectOption({ label: "Message of the day" });
  await page.getByRole("button", { name: "Add Block" }).click();
  await page.getByRole("textbox", { name: "Text", exact: true }).fill("Hello from the e2e test");
  await page.getByRole("textbox", { name: "Reason", exact: true }).fill("e2e: new target");
  await page.getByRole("button", { name: "Show the plan" }).click();

  // The plan, then approve.
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText("guest:config").first()).toBeVisible();
  await expect(page.getByText("file:/etc/motd").first()).toBeVisible();
  await page.getByRole("button", { name: "Approve and apply" }).click();

  // The run: steps arrive from the event stream.
  await expect(page.getByRole("heading", { name: /^Run \d+$/ })).toBeVisible();
  const guestRow = page.getByRole("row", { name: /guest:config/ });
  await expect(guestRow).toContainText("applied");

  // The demo drift issue: revert it.
  await page.getByRole("link", { name: "Issues", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Issues" })).toBeVisible();
  await page
    .getByRole("link", { name: /^Issue \d+$/ })
    .first()
    .click();
  await expect(page.getByText("differs from the desired state")).toBeVisible();
  const issueUrl = page.url();
  await page.getByRole("button", { name: /^Revert/ }).click();

  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await page.getByRole("button", { name: "Approve and apply" }).click();
  await expect(page.getByRole("heading", { name: /^Run \d+$/ })).toBeVisible();
  await expect(page.getByText(/Run status:\s*succeeded/)).toBeVisible({ timeout: 30_000 });

  await page.goto(issueUrl);
  await expect(page.getByText("Status: resolved")).toBeVisible();

  // Log out ends the session.
  await page.getByRole("button", { name: "Log out" }).click();
  await expect(page.getByRole("heading", { name: "Log in to Plumb" })).toBeVisible();
});

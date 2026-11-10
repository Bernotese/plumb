import { expect, type Page } from "@playwright/test";

// Login for the e2e specs after slice.spec.ts. All specs share one server
// (server.mjs). slice.spec.ts changes the one-time password first; a spec
// that runs alone does the change itself.
export const NEW_PASSWORD = "e2e-new-password-1234";

export async function login(page: Page): Promise<void> {
  const first = process.env["PLUMB_E2E_PASSWORD"] ?? "";
  expect(first).not.toBe("");
  await page.goto("/");
  for (const password of [NEW_PASSWORD, first]) {
    await page.getByLabel("User name").fill("admin");
    await page.getByLabel("Password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Log in" }).click();
    const change = page.getByRole("heading", { name: "Change your password" });
    const targets = page.getByRole("heading", { name: "Targets" });
    const failed = page.getByRole("alert");
    await expect(change.or(targets).or(failed)).toBeVisible();
    if (await targets.isVisible()) return;
    if (await change.isVisible()) {
      await page.getByLabel("Current password").fill(password);
      await page.getByLabel("New password", { exact: true }).fill(NEW_PASSWORD);
      await page.getByLabel("Repeat the new password").fill(NEW_PASSWORD);
      await page.getByRole("button", { name: "Change password" }).click();
      await expect(targets).toBeVisible();
      return;
    }
  }
  throw new Error("login failed with both passwords");
}

/** Approves the open Plan view and waits for a succeeded run. */
export async function approveAndRun(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Approve and apply" }).click();
  await expect(page.getByRole("heading", { name: /^Run \d+$/ })).toBeVisible();
  await expect(page.getByText(/Run status:\s*succeeded/)).toBeVisible({ timeout: 45_000 });
}

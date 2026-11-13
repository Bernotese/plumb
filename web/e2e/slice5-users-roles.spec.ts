import { expect, test, type Browser, type Page } from "@playwright/test";
import { login } from "./login";

// T-154 on the real API (T-147; D-329, D-330, D-343): Settings → Users and
// the role-aware buttons with real users. The buttons are comfort only:
// the server refuses a call below the role (403), checked here too.

/** Creates a user in Settings → Users and returns its one-time password. */
async function createUser(page: Page, name: string, role: string): Promise<string> {
  await page.goto("/#/settings/users");
  await page.getByLabel("User name").fill(name);
  await page.getByLabel("Role", { exact: true }).selectOption(role);
  await page.getByRole("button", { name: "Create user" }).click();
  const value = page.locator("#otp-value");
  await expect(value).toBeVisible();
  const password = (await value.textContent()) ?? "";
  expect(password.length).toBeGreaterThan(10);
  await page.getByRole("button", { name: "Done, I copied it" }).click();
  await expect(value).toHaveCount(0);
  return password;
}

/** A new browser context, logged in as the user (forced password change first). */
async function as(browser: Browser, name: string, otp: string): Promise<Page> {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto("/");
  await page.getByLabel("User name").fill(name);
  await page.getByLabel("Password", { exact: true }).fill(otp);
  await page.getByRole("button", { name: "Log in" }).click();
  await page.getByLabel("Current password").fill(otp);
  await page.getByLabel("New password", { exact: true }).fill(`${name}-password-1234`);
  await page.getByLabel("Repeat the new password").fill(`${name}-password-1234`);
  await page.getByRole("button", { name: "Change password" }).click();
  await expect(page.getByRole("heading", { name: "Targets" })).toBeVisible();
  return page;
}

test("users: create, one-time password once, role change, disable, name taken", async ({
  page,
  browser,
}) => {
  await login(page);
  await page.getByRole("link", { name: "Settings" }).click();
  await page.getByRole("link", { name: "Users", exact: true }).click();
  await expect(page.getByRole("row", { name: /^admin\s?\(you\)/ })).toContainText(
    "Change your own password in your session.",
  );

  const otp = await createUser(page, "e2e-reader", "read");
  expect(await page.content()).not.toContain(otp);
  const row = page.getByRole("row", { name: /^e2e-reader/ });
  await expect(row).toContainText("must change password");

  // The user logs in with it and has the role read.
  const reader = await as(browser, "e2e-reader", otp);
  await expect(reader.getByText("user:e2e-reader · read")).toBeVisible();
  await expect(reader.getByRole("link", { name: "Settings" })).toHaveCount(0);

  // Role change: the session of the user ends (401 → login page).
  await page.reload();
  await row.getByRole("button", { name: "Change the role of e2e-reader" }).click();
  await page.locator("#act-e2e-reader").selectOption("plan");
  await expect(page.getByText("Every session of e2e-reader ends.")).toBeVisible();
  await page.getByRole("button", { name: "Change role" }).click();
  await expect(row.getByText("plan", { exact: true })).toBeVisible();
  await reader.goto("/#/plans");
  await expect(reader.getByRole("button", { name: "Log in" })).toBeVisible();
  await reader.context().close();

  // Disable: no login; the name stays taken.
  await row.getByRole("button", { name: "Disable e2e-reader" }).click();
  await page.getByRole("button", { name: "Disable", exact: true }).click();
  await expect(row).toContainText("disabled");
  await page.getByLabel("User name").fill("e2e-reader");
  await page.getByRole("button", { name: "Create user" }).click();
  await expect(page.getByRole("alert")).toContainText(/taken|exists/i);
  await row.getByRole("button", { name: "Enable e2e-reader" }).click();
  await page.getByRole("button", { name: "Enable", exact: true }).click();
  await expect(row).not.toContainText("disabled");
});

test("role read: no write button, and the server refuses a write", async ({ page, browser }) => {
  await login(page);
  const otp = await createUser(page, "e2e-viewer", "read");
  const pending = await page.request.get("/api/v1/plans?status=pending&limit=1");
  const planId = ((await pending.json()) as { items: { id: number }[] }).items[0]?.id;
  const issues = await page.request.get("/api/v1/issues?limit=1");
  const issueId = ((await issues.json()) as { items: { id: number }[] }).items[0]?.id;

  const viewer = await as(browser, "e2e-viewer", otp);
  await expect(viewer.getByRole("link", { name: "New target" })).toHaveCount(0);
  await viewer.goto("/#/blocks");
  await expect(viewer.getByRole("heading", { name: "Starter catalogue" })).toBeVisible();
  await expect(viewer.getByRole("link", { name: "New block" })).toHaveCount(0);
  await expect(viewer.getByRole("button", { name: /^Add / })).toHaveCount(0);
  await expect(viewer.locator("#catalog-role")).toContainText("Your role is read.");
  await viewer.goto("/#/targets/web1/edit");
  await expect(viewer.locator("#t-role")).toBeVisible();
  await expect(viewer.getByRole("button", { name: "Show the plan" })).toHaveCount(0);
  await expect(viewer.getByRole("button", { name: "Delete target…" })).toHaveCount(0);
  if (planId) {
    await viewer.goto(`/#/plans/${planId}`);
    await expect(viewer.locator("#approve-role")).toBeVisible();
    await expect(viewer.getByRole("button", { name: "Approve and apply" })).toHaveCount(0);
    await expect(viewer.getByRole("button", { name: "Drop plan" })).toHaveCount(0);
  }
  if (issueId) {
    await viewer.goto(`/#/issues/${issueId}`);
    await expect(viewer.getByRole("heading", { name: `Issue ${issueId}` })).toBeVisible();
    await expect(viewer.getByRole("button", { name: "Add comment" })).toHaveCount(0);
  }
  // The guard is comfort only: the server says no on its own.
  const origin = new URL(viewer.url()).origin;
  const res = await viewer.request.post("/api/v1/plans", {
    headers: { origin },
    data: { reason: "e2e: read tries", put: [] },
  });
  expect(res.status()).toBe(403);
  await viewer.context().close();
});

test("role apply: a destructive plan needs admin (UI and server)", async ({ page, browser }) => {
  await login(page);
  const otp = await createUser(page, "e2e-operator", "apply");
  const origin = new URL(page.url()).origin;
  const made = await page.request.post("/api/v1/plans", {
    headers: { origin },
    data: { reason: "e2e: destructive for roles", delete: [{ kind: "Target", name: "app1" }] },
  });
  expect(made.status()).toBe(201);
  const plan = (await made.json()) as { id: number; risk: string };
  expect(plan.risk).toBe("destructive");

  const op = await as(browser, "e2e-operator", otp);
  await op.goto(`/#/plans/${plan.id}`);
  await expect(op.locator("#approve-role")).toContainText(
    "A destructive plan needs the role admin.",
  );
  await expect(op.getByRole("button", { name: "Approve and apply" })).toHaveCount(0);
  const refused = await op.request.post(`/api/v1/plans/${plan.id}/approve`, {
    headers: { origin },
    data: {},
  });
  expect(refused.status()).toBe(403);
  await op.getByRole("button", { name: "Drop plan" }).click();
  await op.locator("#drop-confirm").click();
  await expect(op.getByText("This plan is stale.")).toBeVisible();
  await op.context().close();
});

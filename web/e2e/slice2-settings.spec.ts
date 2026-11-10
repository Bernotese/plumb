import { expect, test } from "@playwright/test";
import { login } from "./login";

// T-035 on the real API (T-030 tokens, T-028 secrets). Export and import
// follow with T-033.
test("tokens: create, use once, revoke", async ({ page }) => {
  await login(page);
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await expect(page.getByRole("link", { name: "API tokens" })).toHaveAttribute(
    "aria-current",
    "page",
  );
  await expect(page.getByText("An apply token is root on the guests.")).toBeVisible();

  await page.getByLabel("Name", { exact: true }).fill("e2e token");
  await page.getByLabel("Tier").selectOption("apply");
  await page.getByLabel("Max risk it can approve").selectOption("reversible");
  await page.getByRole("button", { name: "Create token" }).click();

  await expect(page.getByText("You cannot see it again.")).toBeVisible();
  const bearer = ((await page.locator("#bearer-value").textContent()) ?? "").trim();
  expect(bearer).toMatch(/^\S+\.\S+$/);
  expect(page.url()).not.toContain(bearer);

  // The bearer works against the API.
  const auth = { authorization: `Bearer ${bearer}` };
  expect((await page.request.get("/api/v1/targets", { headers: auth })).status()).toBe(200);

  await page.getByRole("button", { name: "Done, I copied it" }).click();
  await expect(page.locator("#bearer-value")).toHaveCount(0);
  const row = page.getByRole("row", { name: /e2e token/ });
  await expect(row).toContainText("apply");
  await expect(row).toContainText("reversible");
  await expect(row).toContainText("Active");

  await row.getByRole("button", { name: "Revoke e2e token" }).click();
  await page.getByRole("button", { name: "Revoke token" }).click();
  await expect(row).toContainText("Revoked");
  expect((await page.request.get("/api/v1/targets", { headers: auth })).status()).toBe(401);
});

test("secrets: set, replace, delete; no value comes back", async ({ page }) => {
  const value = "e2e-secret-value-7b1f";
  await login(page);
  await page.goto("/#/settings/secrets");
  await expect(page.getByRole("heading", { name: "Set a secret" })).toBeVisible();
  await expect(page.getByLabel("Value", { exact: true })).toHaveAttribute(
    "autocomplete",
    "new-password",
  );

  await page.getByLabel("Name", { exact: true }).fill("e2e-secret");
  await page.getByLabel("Value", { exact: true }).fill(value);
  await page.getByRole("button", { name: "Save secret" }).click();
  const row = page.getByRole("row", { name: /e2e-secret/ });
  await expect(row).toContainText("user:admin");
  await expect(page.getByLabel("Value", { exact: true })).toHaveValue("");
  const v1 = Number(((await row.getByRole("cell").first().textContent()) ?? "").trim());
  expect(v1).toBeGreaterThan(0);

  await row.getByRole("button", { name: "Replace e2e-secret" }).click();
  await page.getByLabel("Value", { exact: true }).fill(`${value}-2`);
  await page.getByRole("button", { name: "Replace value" }).click();
  await expect(row.getByRole("cell").first()).not.toHaveText(String(v1));

  // The API never returns a value.
  const list = await page.request.get("/api/v1/secrets");
  expect(list.status()).toBe(200);
  expect(await list.text()).not.toContain(value);
  expect(await page.content()).not.toContain(value);

  await row.getByRole("button", { name: "Delete e2e-secret" }).click();
  await page.getByRole("button", { name: "Delete secret" }).click();
  await expect(page.getByRole("row", { name: /e2e-secret/ })).toHaveCount(0);
});

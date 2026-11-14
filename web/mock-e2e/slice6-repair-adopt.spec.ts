import { expect, test } from "@playwright/test";

// T-194 against the mock build (contract of api/openapi.yaml). serve --dev
// has no dead agent: the dead-agent repair flow is proven here; the real
// flow is proven in the CI guest test of T-191 and on the host (T-192).
// Run with `npx playwright test -c playwright.mock.config.ts slice6-repair-adopt`.

test("repair: a dead agent → human-only plan → run → the agent is back", async ({ page }) => {
  await page.goto("/?as=admin#/targets");
  await expect(page.locator("#repair-mark-web3")).toHaveText("agent offline · repair possible");
  await page.locator("#repair-mark-web3").click();
  await expect(page.locator("#repair")).toContainText("ready");
  await page.getByLabel("Reason (optional)").fill("The agent died after a full disk.");
  await page.getByRole("button", { name: "Propose repair" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(
    page.getByText("Repair of a dead agent over SSH (no document change)"),
  ).toBeVisible();
  await expect(page.getByText("Only a person can approve this plan.")).toBeVisible();
  await page.getByRole("button", { name: "Approve and apply" }).click();
  await expect(page.getByText(/Run status:\s*succeeded/)).toBeVisible({ timeout: 30_000 });
  await page.goto("/?as=admin#/targets/web3/agent");
  await expect(page.locator("#repair-not-needed")).toBeVisible();
});

test("adopt: a guest of the pool → plan → Target without agent → install command once", async ({
  page,
}) => {
  await page.goto("/?as=admin#/targets");
  await page.getByRole("link", { name: "Adopt a guest" }).click();
  await expect(page.getByRole("heading", { name: "Adopt a guest" })).toBeVisible();
  await expect(page.locator("#guest-9124-state")).toContainText("it is a privileged container");
  await page.getByRole("button", { name: "Adopt wiki (9123)" }).click();
  await expect(page.locator("#adopt-warnings")).toContainText("Not sealed");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText("Adoption of a guest of the pool as a new Target")).toBeVisible();
  await page.getByRole("button", { name: "Approve and apply" }).click();
  await expect(page.getByText(/Run status:\s*succeeded/)).toBeVisible({ timeout: 30_000 });

  await page.goto("/?as=admin#/targets");
  const row = page.getByRole("row", { name: /^wiki/ });
  await expect(row).toContainText("adopted");
  await page.goto("/?as=admin#/targets/adopt");
  await expect(page.getByRole("button", { name: "Adopt wiki (9123)" })).toHaveCount(0);

  await page.goto("/?as=admin#/targets/wiki/agent");
  await page.getByRole("button", { name: "Make an install command" }).click();
  await expect(page.locator("#install-command-text")).toContainText("<<'PLUMB_TOKEN'");
  await expect(page.locator("#install-command")).toContainText("This command is a secret until");
  await page.getByRole("button", { name: "Done, I copied it" }).click();
  await expect(page.locator("#install-command-text")).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("PLUMB_TOKEN");
});

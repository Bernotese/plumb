import { expect, test } from "@playwright/test";
import { login } from "./login";

// T-097 on the real API (T-092): the simulated agents of the demo server
// run the served binary, so the card says "current" and offers no update.
test("agent: version and state of a current agent", async ({ page }) => {
  await login(page);
  await page.goto("/#/targets/web1/agent");
  await expect(page.getByRole("heading", { name: "Agent of web1" })).toBeVisible();
  const card = page.getByRole("region", { name: "Agent of web1" });
  await expect(card).toContainText("The agent is current");
  await expect(card.locator("dd.mono")).not.toHaveText("–");
  await expect(page.getByRole("button", { name: "Propose agent update" })).toHaveCount(0);

  await page.getByRole("link", { name: "Targets", exact: true }).click();
  await expect(page.getByRole("row", { name: /^web1 / })).toContainText(/agent \S+/);
});

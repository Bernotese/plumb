import { expect, test, type Page } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-154 on the real API: the starter catalogue (D-328, T-146). Users and
// roles: slice5-users-roles.spec.ts.

/** The row of a catalogue Block (the second table of the Blocks page). */
const catalogRow = (page: Page, name: string) =>
  page
    .getByRole("region", { name: "Starter catalogue, scrolls sideways" })
    .getByRole("row", { name: new RegExp(`^${name}\\b`) });

test("catalogue: add a starter Block by a plan, update offers the difference", async ({ page }) => {
  await login(page);
  await page.getByRole("link", { name: "Blocks", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Starter catalogue" })).toBeVisible();

  const tools = catalogRow(page, "common-tools");
  await expect(tools).toContainText("not added");
  await tools.getByRole("button", { name: "Add common-tools" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(
    page.getByText("Add the starter Block common-tools from the catalogue."),
  ).toBeVisible();
  // Not stored before the approval.
  expect((await page.request.get("/api/v1/blocks/common-tools")).status()).toBe(404);
  await approveAndRun(page);
  await page.getByRole("link", { name: "Blocks", exact: true }).click();
  await expect(catalogRow(page, "common-tools")).toContainText("added, same");
  await expect(page.getByRole("link", { name: "common-tools", exact: true })).toBeVisible();

  // A stored Block that differs: the difference, then an update plan (not applied).
  const motd = catalogRow(page, "motd");
  await expect(motd).toContainText("added, differs");
  await motd.getByRole("button", { name: "Show difference" }).click();
  await expect(page.getByRole("region", { name: "Changed Block motd" })).toBeVisible();
  // The demo motd uses the parameter "message" like the catalogue (T-161),
  // so the update is a normal plan; it is dropped, not applied.
  await motd.getByRole("button", { name: "Update from the catalogue: motd" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText("Update the Block motd from the catalogue.")).toBeVisible();
  await page.getByRole("button", { name: "Drop plan" }).click();
  await page.locator("#drop-confirm").click();
  await expect(page.getByText("This plan is stale.")).toBeVisible();
});

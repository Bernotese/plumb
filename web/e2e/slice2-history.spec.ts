import { expect, test, type Page } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-034 on the real API (T-031): change a target twice, read the diff in
// History, roll back to the first change and approve the rollback plan.
async function setMotd(page: Page, text: string, reason: string): Promise<void> {
  await page.getByRole("link", { name: "Targets", exact: true }).click();
  await page.getByRole("link", { name: "web1", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Edit target web1" })).toBeVisible();
  await page.getByRole("textbox", { name: "Text", exact: true }).fill(text);
  await page.getByRole("textbox", { name: "Reason", exact: true }).fill(reason);
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await approveAndRun(page);
}

/** The revision number of the History row whose reason is `reason`. */
async function revisionOf(page: Page, reason: string): Promise<number> {
  const row = page.getByRole("row", { name: new RegExp(reason) });
  const link = row.getByRole("link", { name: /^Revision \d+$/ });
  return Number(/\d+/.exec((await link.textContent()) ?? "")?.[0]);
}

test("history: diff of two changes, rollback through a plan", async ({ page }) => {
  await login(page);
  await setMotd(page, "e2e history one", "e2e: motd one");
  await setMotd(page, "e2e history two", "e2e: motd two");

  await page.getByRole("link", { name: "History", exact: true }).click();
  await expect(page.getByRole("heading", { name: "History" })).toBeVisible();
  const head = page.getByRole("row").nth(1);
  await expect(head).toContainText("e2e: motd two");
  await expect(head).toContainText("(head)");
  await expect(head).toContainText("user:admin");
  const one = await revisionOf(page, "e2e: motd one");
  const two = await revisionOf(page, "e2e: motd two");
  expect(two).toBe(one + 1);

  // The diff of the second change: one line out, one line in.
  await head.getByRole("link", { name: `Revision ${two}` }).click();
  await expect(page.getByRole("heading", { name: `Revision ${two}` })).toBeVisible();
  await expect(page.getByText(`Changes from revision ${one} to revision ${two}`)).toBeVisible();
  const doc = page.getByRole("region", { name: "YAML diff of Target web1" });
  await expect(doc.locator("tr.del")).toHaveCount(1);
  await expect(doc.locator("tr.del")).toContainText("e2e history one");
  await expect(doc.locator("tr.add")).toHaveCount(1);
  await expect(doc.locator("tr.add")).toContainText("e2e history two");

  // Roll back to the first change.
  await page.goto(`/#/revisions/${one}`);
  await expect(page.getByRole("heading", { name: `Revision ${one}` })).toBeVisible();
  await page.getByRole("button", { name: "Roll back to this revision" }).click();
  await expect(page.getByText("Give a reason for the rollback.")).toBeVisible();
  await page.getByLabel("Reason (required)").fill("e2e: back to motd one");
  await page.getByRole("button", { name: "Roll back to this revision" }).click();

  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText(`Rollback to revision ${one}`)).toBeVisible();
  await expect(page.getByRole("row", { name: /message|content/ }).first()).toBeVisible();
  await approveAndRun(page);

  // The new head has the documents of revision `one` again.
  await page.getByRole("link", { name: "History", exact: true }).click();
  const back = page.getByRole("row").nth(1);
  await expect(back).toContainText("e2e: back to motd one");
  await back.getByRole("link", { name: /^Revision \d+$/ }).click();
  const diff = page.getByRole("region", { name: "YAML diff of Target web1" });
  await expect(diff.locator("tr.del")).toContainText("e2e history two");
  await expect(diff.locator("tr.add")).toContainText("e2e history one");

  // Rolling back to the head again has nothing to do.
  await page.getByLabel("Reason (required)").fill("e2e: no-op");
  await page.getByRole("button", { name: "Roll back to this revision" }).click();
  await expect(page.getByRole("status")).toContainText("Nothing to roll back.");
});

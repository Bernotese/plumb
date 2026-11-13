import { expect, test, type Page } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-154 on the real API: the starter catalogue (D-328, T-146) and the
// role-aware buttons (D-329). Until T-147 the server gives every session
// the role admin, so the role test answers GET /session itself; every other
// call goes to the real server. The UI guard is comfort only.

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
  // The catalogue motd names its parameter "message"; the demo Targets use
  // "text", so the server refuses the update and the row shows why.
  await motd.getByRole("button", { name: "Update from the catalogue: motd" }).click();
  await expect(motd.getByRole("alert")).toContainText('has no parameter "text"');
  await expect(page).toHaveURL(/#\/blocks$/);
});

/** Answers GET /session with the given role (after a login). */
async function loginAs(page: Page, tier: "read" | "plan" | "apply", fresh = true) {
  if (fresh) await login(page);
  await page.route("**/api/v1/session", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    const res = await route.fetch();
    const body = (await res.json()) as Record<string, unknown>;
    await route.fulfill({ response: res, json: { ...body, tier } });
  });
  await page.reload();
  await expect(page.getByText(`· ${tier}`)).toBeVisible();
}

test("role read: no write button on the main screens", async ({ page }) => {
  await loginAs(page, "read");
  await page.goto("/#/targets");
  await expect(page.getByRole("heading", { name: "Targets" })).toBeVisible();
  await expect(page.getByRole("link", { name: "New target" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Settings" })).toHaveCount(0);

  await page.goto("/#/blocks");
  await expect(page.getByRole("heading", { name: "Starter catalogue" })).toBeVisible();
  await expect(page.getByRole("link", { name: "New block" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^Add / })).toHaveCount(0);
  await expect(page.locator("#catalog-role")).toContainText("Your role is read.");

  await page.goto("/#/targets/web1/edit");
  await expect(page.locator("#t-role")).toBeVisible();
  await expect(page.getByRole("button", { name: "Show the plan" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Delete target…" })).toHaveCount(0);

  const res = await page.request.get("/api/v1/plans?status=pending&limit=1");
  const pending = ((await res.json()) as { items: { id: number }[] }).items[0];
  if (pending) {
    await page.goto(`/#/plans/${pending.id}`);
    await expect(page.locator("#approve-role")).toBeVisible();
    await expect(page.getByRole("button", { name: "Approve and apply" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Drop plan" })).toHaveCount(0);
  }

  const issues = await page.request.get("/api/v1/issues?limit=1");
  const issue = ((await issues.json()) as { items: { id: number }[] }).items[0];
  if (issue) {
    await page.goto(`/#/issues/${issue.id}`);
    await expect(page.getByRole("heading", { name: `Issue ${issue.id}` })).toBeVisible();
    await expect(page.getByRole("button", { name: "Add comment" })).toHaveCount(0);
  }
});

test("role apply: a destructive plan needs admin", async ({ page }) => {
  await login(page);
  const origin = new URL(page.url()).origin;
  const made = await page.request.post("/api/v1/plans", {
    headers: { origin },
    data: { reason: "e2e: destructive for roles", delete: [{ kind: "Target", name: "app1" }] },
  });
  expect(made.status()).toBe(201);
  const plan = (await made.json()) as { id: number; risk: string };
  expect(plan.risk).toBe("destructive");
  await loginAs(page, "apply", false);
  await page.goto(`/#/plans/${plan.id}`);
  await expect(page.locator("#approve-role")).toContainText(
    "A destructive plan needs the role admin.",
  );
  await expect(page.getByRole("button", { name: "Approve and apply" })).toHaveCount(0);
  await page.getByRole("button", { name: "Drop plan" }).click();
  await page.locator("#drop-confirm").click();
  await expect(page.getByText("This plan is stale.")).toBeVisible();
});

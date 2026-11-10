import { expect, test, type Page } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-036 on the real API (T-032): ignore once and ignore by rule. The dev
// demo seeds drift on web1: the nginx site and the nginx package
// (cmd/plumb/dev.go). This spec runs before slice2-history.spec.ts, whose
// plans of web1 revert all drift of web1.
const ORIGIN = { origin: "http://127.0.0.1:18460" };

/** Opens the open Issue of web1 for `resource`. */
async function openIssue(page: Page, resource: string): Promise<void> {
  await page.getByRole("link", { name: "Issues", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Issues" })).toBeVisible();
  const row = page.getByRole("row", { name: new RegExp(resource.replace(/[/.]/g, "\\$&")) });
  await row.getByRole("link", { name: /^Issue \d+$/ }).click();
  await expect(page.getByText(resource).first()).toBeVisible();
  await expect(page.getByText("Status: open")).toBeVisible();
}

test("ignore once: confirm, resolved without a plan", async ({ page }) => {
  await login(page);
  await openIssue(page, "package:nginx");
  await page.getByLabel("Reason (optional)").fill("e2e: removed on purpose");
  await page.getByRole("button", { name: "Ignore once" }).click();
  await expect(page.getByText("Ignore this drift?")).toBeVisible();
  await expect(page.getByRole("button", { name: "Ignore this drift" })).toBeFocused();
  await page.getByRole("button", { name: "Ignore this drift" }).click();
  await expect(page.getByRole("status")).toContainText("Ignored once.");
  await expect(page.getByText("Status: resolved")).toBeVisible();
  await expect(page.getByText(/Resolved: ignored by user:admin/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Ignore once" })).toHaveCount(0);
});

test("ignore by rule: human plan, rules list, ignored drift in the next plan", async ({ page }) => {
  const site = "file:/etc/nginx/sites-enabled/default";
  await login(page);
  await openIssue(page, site);
  const issueUrl = page.url();
  await page.getByLabel(/Only the changed attributes/).check();
  await page.getByRole("button", { name: "Make ignore-rule plan" }).click();

  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText("Ignore rule for an issue")).toBeVisible();
  await expect(page.getByText("It adds, changes or removes an ignore rule.")).toBeVisible();
  await approveAndRun(page);

  await page.goto(issueUrl);
  await expect(page.getByText("Status: resolved")).toBeVisible();
  await expect(page.getByText(/Resolved: ignored/)).toBeVisible();

  // The rules list shows the new rule.
  await page.goto("/#/issues/rules");
  await expect(page.getByRole("heading", { name: "Ignore rules" })).toBeVisible();
  const rule = page.getByRole("row", { name: new RegExp(site.replace(/[/.]/g, "\\$&")) });
  await expect(rule).toContainText("web1");
  await expect(rule).toContainText("content");
  await expect(rule.getByRole("link", { name: /^Revision \d+$/ })).toBeVisible();

  // The next Plan of web1 marks the ignored drift; it overwrites it.
  await page.getByRole("link", { name: "Targets", exact: true }).click();
  await page.getByRole("link", { name: "web1", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Edit target web1" })).toBeVisible();
  await page.getByRole("textbox", { name: "Text", exact: true }).fill("e2e: web1 after the rule");
  await page.getByRole("textbox", { name: "Reason", exact: true }).fill("e2e: next plan");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  const siteCard = page.getByRole("region", { name: new RegExp(site.replace(/[/.]/g, "\\$&")) });
  await expect(siteCard).toContainText("A rule ignores this drift; this apply overwrites it.");
  const next = Number(/\d+$/.exec(page.url())?.[0]);
  expect(
    (await page.request.delete(`/api/v1/plans/${next}`, { headers: ORIGIN })).status(),
  ).toBeLessThan(300);

  // Remove the rule through a plan.
  await page.goto("/#/issues/rules");
  const name = ((await rule.getByRole("rowheader").textContent()) ?? "").trim();
  await rule.getByRole("button", { name: /Remove/ }).click();
  await page.getByLabel("Reason (required)").fill("e2e: rule not needed");
  await page.getByRole("button", { name: "Make removal plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText("It adds, changes or removes an ignore rule.")).toBeVisible();
  await approveAndRun(page);
  await page.goto("/#/issues/rules");
  await expect(page.getByRole("heading", { name: "Ignore rules" })).toBeVisible();
  await expect(page.getByRole("rowheader", { name })).toHaveCount(0);
});

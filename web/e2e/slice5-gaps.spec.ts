import { expect, test, type Page } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-153 on the real API (D-338): the web gaps that need no new API. The spec
// makes its own documents (prefix gaps-) so the state of earlier specs does
// not matter; each delete goes through a proposal and the plan preview.

/** A proposal through the API with the session of the page; returns the Plan id. */
async function propose(page: Page, body: unknown): Promise<number> {
  const origin = new URL(page.url()).origin;
  const res = await page.request.post("/api/v1/plans", { data: body, headers: { origin } });
  expect(res.status(), await res.text()).toBe(201);
  return ((await res.json()) as { id: number }).id;
}

/** Proposes and applies through the UI approve button. */
async function apply(page: Page, body: unknown): Promise<void> {
  const id = await propose(page, body);
  await page.goto(`/#/plans/${id}`);
  await approveAndRun(page);
}

const block = {
  kind: "Block",
  name: "gaps-note",
  spec: {
    title: "Gaps note",
    params: { type: "object", properties: {} },
    resources: [{ file: { path: "/etc/gaps-note", content: "gaps\n", mode: "0644" } }],
  },
};

test("delete a Block, an Image and a Target by a proposal", async ({ page }) => {
  test.setTimeout(180_000);
  await login(page);
  await apply(page, {
    reason: "e2e: gaps setup",
    put: [
      block,
      {
        kind: "Image",
        name: "gaps-img",
        spec: {
          type: "vm",
          title: "Gaps image",
          url: "https://cloud-images.ubuntu.com/releases/26.04/release/gaps-cloudimg-amd64.img",
          sha256: "1".repeat(64),
        },
      },
      {
        kind: "Target",
        name: "gaps-vm",
        spec: {
          type: "vm",
          image: "ubuntu-26.04",
          guest: { cores: 1, memory: 1024, disk: 16, bridge: "vmbr0" },
          blocks: [{ use: "gaps-note" }],
        },
      },
    ],
  });

  // A Block in use: no delete button, the reason is shown.
  await page.goto("/#/blocks/gaps-note");
  await expect(page.getByRole("heading", { name: "Delete this block" })).toBeVisible();
  await expect(page.getByText("Remove it from them first.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Delete block…" })).toHaveCount(0);

  // The Target: the plan deletes the guest, destructive, and only after approve.
  await page.goto("/#/targets/gaps-vm/edit");
  await page.getByRole("button", { name: "Delete target…" }).click();
  await expect(page.getByText("and its disks in Proxmox")).toBeVisible();
  await page.getByRole("button", { name: "Make delete plan" }).click();
  await expect(page.getByText("Give a reason for the delete.")).toBeVisible();
  await page.getByLabel("Reason (required)").fill("e2e: delete gaps-vm");
  await page.getByRole("button", { name: "Make delete plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: /Delete\s+guest:config/ })).toBeVisible();
  await expect(page.locator(".facts")).toContainText("destructive");
  // Not deleted before the approval.
  const before = await page.request.get("/api/v1/targets/gaps-vm");
  expect(before.status()).toBe(200);
  await approveAndRun(page);
  await page.goto("/#/targets");
  await expect(page.getByRole("heading", { name: "Targets" })).toBeVisible();
  await expect(page.getByRole("link", { name: "gaps-vm", exact: true })).toHaveCount(0);

  // The Block is free now.
  await page.goto("/#/blocks/gaps-note");
  await page.getByRole("button", { name: "Delete block…" }).click();
  await page.getByLabel("Reason (required)").fill("e2e: delete gaps-note");
  await page.getByRole("button", { name: "Make delete plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await approveAndRun(page);
  await page.goto("/#/blocks");
  await expect(page.getByRole("heading", { name: "Blocks" })).toBeVisible();
  await expect(page.getByRole("link", { name: "gaps-note", exact: true })).toHaveCount(0);

  // The Image: no Target uses it.
  await page.goto("/#/images/gaps-img");
  await page.getByRole("button", { name: "Delete image…" }).click();
  await page.getByLabel("Reason (required)").fill("e2e: delete gaps-img");
  await page.getByRole("button", { name: "Make delete plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await approveAndRun(page);
  expect((await page.request.get("/api/v1/images/gaps-img")).status()).toBe(404);

  // An Image in use names its Targets.
  await page.goto("/#/images/ubuntu-26.04");
  await expect(page.locator("#delete-image-blocked")).toContainText("Used by");
});

test("plan list with drop, run list", async ({ page }) => {
  await login(page);
  const id = await propose(page, { reason: "e2e: gaps plan to drop", put: [block] });
  await page.getByRole("link", { name: "Plans", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Plans" })).toBeVisible();
  const row = page.getByRole("row", { name: new RegExp(`^Plan ${id}\\b`) });
  await expect(row).toContainText("pending");
  await expect(row).toContainText("e2e: gaps plan to drop");
  await row.getByRole("button", { name: `Drop plan ${id}` }).click();
  await page.locator(`#drop-confirm-${id}`).click();
  await expect(row).toHaveCount(0);
  await page.locator("#plan-filter").selectOption("stale");
  await expect(page.getByRole("row", { name: new RegExp(`^Plan ${id}\\b`) })).toContainText(
    "stale",
  );
  await page.goto(`/#/plans/${id}`);
  await expect(page.getByText("This plan is stale.")).toBeVisible();

  // Drop from the plan page itself.
  const id2 = await propose(page, { reason: "e2e: gaps plan to drop 2", put: [block] });
  await page.goto(`/#/plans/${id2}`);
  await page.getByRole("button", { name: "Drop plan" }).click();
  await page.locator("#drop-confirm").click();
  await expect(page.getByText("This plan is stale.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Approve and apply" })).toHaveCount(0);

  await page.getByRole("link", { name: "Runs", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Runs" })).toBeVisible();
  const first = page.locator("tbody tr").first();
  await expect(first).toContainText(/succeeded|failed|running/);
  await first.getByRole("link", { name: /^Run \d+$/ }).click();
  await expect(page.getByRole("heading", { name: /^Run \d+$/ })).toBeVisible();
});

test("issue comment and filter by target", async ({ page }) => {
  await login(page);
  const res = await page.request.get("/api/v1/issues?limit=1");
  const issue = ((await res.json()) as { items: { id: number; target: string }[] }).items[0];
  expect(issue, "the demo has issues").toBeTruthy();
  if (!issue) return;

  await page.goto(`/#/issues/${issue.id}`);
  await page.getByLabel("Add a comment").fill("e2e: checked by hand.\nSecond line.");
  await page.getByRole("button", { name: "Add comment" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Comment added." })).toBeVisible();
  await expect(page.getByText("e2e: checked by hand.")).toBeVisible();
  await page.reload();
  await expect(page.getByText("e2e: checked by hand.")).toBeVisible();

  await page.goto("/#/issues");
  await page.locator("#issue-filter").selectOption("all");
  await page.locator("#issue-target").selectOption(issue.target);
  await expect(page).toHaveURL(new RegExp(`#/issues\\?target=${issue.target}$`));
  const cells = page.locator("tbody tr td:first-of-type");
  // Wait until the filtered list is shown: no row of another Target (T-197).
  await expect(cells.first()).toHaveText(issue.target);
  await expect(cells.filter({ hasNotText: issue.target })).toHaveCount(0);
  for (const t of await cells.allTextContents()) expect(t).toBe(issue.target);
});

test("compare two revisions", async ({ page }) => {
  await login(page);
  const res = await page.request.get("/api/v1/revisions?limit=1");
  const head = ((await res.json()) as { items: { id: number }[] }).items[0]?.id ?? 0;
  expect(head).toBeGreaterThan(2);
  await page.goto(`/#/revisions/${head}`);
  await expect(
    page.getByText(`Changes from revision ${head - 1} to revision ${head}`),
  ).toBeVisible();
  await page.getByLabel("Base revision").fill("1");
  await page.getByRole("button", { name: "Compare" }).click();
  await expect(page).toHaveURL(new RegExp(`#/revisions/${head}\\?from=1$`));
  await expect(page.getByText(`Changes from revision 1 to revision ${head}`)).toBeVisible();
  await page.getByRole("link", { name: "With the revision before" }).click();
  await expect(
    page.getByText(`Changes from revision ${head - 1} to revision ${head}`),
  ).toBeVisible();
});

import { expect, test } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-064 on the real API of T-062: make a built Image in the form, approve
// its Plan (the build of v1 runs in the same Run), then "Build now" makes
// v2. The dev server runs a simulated agent in each build guest: it seals
// and powers the guest off, like the real agent (T-060).
test("images: make a built image, build v1 and v2", async ({ page }) => {
  await login(page);
  await page.getByRole("link", { name: "Images", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Images" })).toBeVisible();

  await page.goto("/#/images/new");
  await expect(page.getByRole("heading", { name: "New image" })).toBeVisible();
  await page.getByLabel("Name").fill("e2e-built");
  await page.getByLabel("Download address").fill("https://images.invalid/e2e.img");
  await page.getByLabel("SHA-256 checksum").fill("c".repeat(64));
  await page.getByLabel("Build templates from this image").check();
  await page.getByLabel("Block to add").selectOption({ label: "Message of the day" });
  await page.getByRole("button", { name: "Add Block" }).click();
  const block = page.getByRole("group", { name: "Message of the day" });
  await block.getByLabel("Text").fill("built by e2e");
  await page.locator("#i-guest-disk").fill("10");
  await page.locator("#i-guest-bridge").fill("vmbr0");
  await page.getByLabel("Reason").fill("e2e: built image");
  await page.getByRole("button", { name: "Show the plan" }).click();

  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText("A person must approve: image recipe")).toBeVisible();
  await expect(page.getByText(/e2e-built/).first()).toBeVisible();
  await approveAndRun(page);
  await expect(page.getByText("image:seal").first()).toBeVisible();

  await page.goto("/#/images/e2e-built");
  await expect(page.getByRole("heading", { name: /e2e-built/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /^v1 ?ready/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Left templates" })).toHaveCount(0);

  await page.getByLabel("Reason (optional)").fill("e2e: second version");
  await page.getByRole("button", { name: "Build now" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await approveAndRun(page);

  await page.goto("/#/images/e2e-built");
  await expect(page.getByRole("row", { name: /^v2 ?ready/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /^v1 ?ready/ })).toBeVisible();
  await page.goto("/#/images");
  await expect(page.getByRole("row", { name: /e2e-built/ })).toContainText("v2");
});

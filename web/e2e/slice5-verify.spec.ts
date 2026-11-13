import { expect, test } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-154 on the real API (T-152 part A; D-335, D-344, D-345): an Image with a
// verify source and a SHA-512 VM pin. "Refresh base" itself (POST
// /images/{name}/refresh, T-152 part B) is tested on the mock build in
// mock-e2e/slice5-verify.spec.ts until the server has it.

test("image form: Debian SHA-512 pin with verify, checked in browser and server", async ({
  page,
}) => {
  await login(page);
  await page.goto("/#/images/new");
  await page.getByLabel("Name").fill("e2e-debian-verify");
  await page
    .getByLabel("Download address")
    .fill("https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2");
  // A source that does not fit the address: refused in the browser.
  await page.locator("#i-sha").fill("c".repeat(64));
  await page.locator("#i-verify").selectOption("ubuntu-cloud");
  await page.locator("#i-reason").fill("e2e: Debian with SHA512SUMS");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.locator("#i-verify-error")).toContainText("cloud-images.ubuntu.com");

  await page.locator("#i-verify").selectOption("debian-https");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.locator("#i-verify-error")).toContainText("SHA-512");
  await page.locator("#i-algo").selectOption("sha512");
  await page.locator("#i-sha").fill("c".repeat(128));
  await expect(page.getByText("every new pin needs a person")).toBeVisible();
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await approveAndRun(page);

  await page.goto("/#/images/e2e-debian-verify");
  await expect(page.locator("#image-sum")).toContainText(`sha512 ${"c".repeat(128)}`);
  await expect(page.locator("#image-verify")).toContainText("not signed by Debian");
  await expect(page.locator("#refresh-unsigned")).toBeVisible();
  await expect(page.getByRole("button", { name: "Refresh base" })).toBeVisible();

  await page.goto("/#/images/e2e-debian-verify/edit");
  await expect(page.locator("#i-verify")).toHaveValue("debian-https");
  await expect(page.locator("#i-algo")).toHaveValue("sha512");
});

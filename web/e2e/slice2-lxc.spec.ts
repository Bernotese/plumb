import { expect, test } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-081 on the real API (T-075 rules): an lxc Image from a known Proxmox
// template needs a person (D-235); a Target of it is a container with no
// bootstrap user and keys for root (D-226).
test("lxc: image from a known template, container target form", async ({ page }) => {
  await login(page);
  await page.goto("/#/images/new");
  await expect(page.getByRole("heading", { name: "New image" })).toBeVisible();
  await page.getByLabel("Name").fill("e2e-ct");
  await page.getByLabel("Container (LXC)").check();
  // T-082: an lxc Image may have a build; it is off by default.
  await expect(page.getByLabel("Build templates from this image")).not.toBeChecked();
  await page.getByLabel("Known template").selectOption({ label: "Debian 13" });
  await expect(page.getByLabel("Download address")).toHaveValue(
    /^http:\/\/download\.proxmox\.com\//,
  );
  await expect(page.getByLabel("SHA-512 checksum")).toHaveValue(/^[0-9a-f]{128}$/);
  await page.getByLabel("Reason").fill("e2e: container image");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText("Only a person can approve this plan.")).toBeVisible();
  await expect(page.getByText(/makes a container image/)).toBeVisible();
  await approveAndRun(page);

  await page.goto("/#/images/e2e-ct");
  await expect(page.locator("#image-type")).toHaveText("Container (LXC)");
  await expect(page.locator("#image-sum")).toHaveText(/^sha512 [0-9a-f]{128}$/);

  await page.goto("/#/targets/new");
  await expect(page.getByRole("heading", { name: "New target" })).toBeVisible();
  await page.getByLabel("Image").selectOption({ value: "e2e-ct" });
  await expect(page.locator("#t-type")).toHaveText("Container (LXC)");
  await expect(page.locator("#guest-user")).toHaveCount(0);
  await expect(page.getByText("SSH keys for root")).toBeVisible();
  await page.getByLabel("Name").fill("e2e-ct1");
  await page.locator("#guest-disk").fill("1");
  await page.getByLabel("Reason").fill("e2e: container");
  await page.getByRole("button", { name: "Show the plan" }).click();
  // The server rule of T-075: a container root fs needs at least 2 GiB.
  await expect(page.getByText(/at least 2/i).first()).toBeVisible();
  await page.locator("#guest-disk").fill("4");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText(/e2e-ct1/).first()).toBeVisible();
});

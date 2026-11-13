import { expect, test } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-118 on the real API (T-115; D-291, D-299): day 2 of a Target (disk
// grow, tags, power) and the Block picker filtered by the target kind
// (D-260). web2: no later spec plans it; it ends stopped. The kind filter uses the
// container Image e2e-ct of slice2-lxc.spec.ts.

test("day 2: disk grows only, tags, a stop needs allow_restart", async ({ page }) => {
  test.setTimeout(150_000);
  await login(page);
  await page.goto("/#/targets/web2/edit");
  await expect(page.getByRole("heading", { name: "Edit target web2" })).toBeVisible();
  // The seed does not manage power (D-299: missing = not managed).
  await expect(page.locator("#guest-power")).toHaveValue("");

  // Browser checks: a smaller disk, a stop without allow_restart.
  await page.locator("#guest-disk").fill("10");
  await page.locator("#guest-power").selectOption("stopped");
  await page.locator("#t-reason").fill("e2e: day 2");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByText("A disk only grows: at least 20 GiB")).toBeVisible();
  await expect(page.getByText(/^A stop needs '.+' below\.$/)).toBeVisible();
  await expect(page).toHaveURL(/#\/targets\/web2\/edit$/);

  // A grow: the Plan shows the D-299 note. It is not approved: the dev
  // agent (linktest) has no grow command, so the Run would fail.
  await page.locator("#guest-disk").fill("24");
  await page.locator("#guest-power").selectOption("");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText("A grow cannot be undone").first()).toBeVisible();

  // A tag: applied.
  await page.goto("/#/targets/web2/edit");
  await expect(page.locator("#guest-disk")).toHaveValue("20");
  await page.locator("#guest-tags-add").click();
  await page.locator("#guest-tags-0").fill("e2e");
  await page.locator("#t-reason").fill("e2e: tag web2");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await approveAndRun(page);
  await page.goto("/#/targets/web2/edit");
  await expect(page.locator("#guest-tags-0")).toHaveValue("e2e");

  // A stop with allow_restart: the Target shows Stopped, not Offline.
  await page.locator("#guest-power").selectOption("stopped");
  await page.locator("#t-allow-restart").check();
  await page.locator("#t-reason").fill("e2e: stop web2");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByText(/Plumb stops the guest at the end of the Run/).first()).toBeVisible();
  await approveAndRun(page);
  await page.getByRole("link", { name: "Targets", exact: true }).click();
  await expect(page.getByRole("row", { name: /web2/ })).toContainText("Stopped");
  // web2 stays stopped: the dev agent does not come back after a start,
  // so a Run of web2 would wait for it. No later spec plans web2.
});

test("kind filter: a sysctl Block is not offered for a container", async ({ page }) => {
  await login(page);
  await page.goto("/#/blocks/new");
  await expect(page.getByRole("heading", { name: "New block" })).toBeVisible();
  await page
    .getByRole("textbox", { name: "Block (YAML)" })
    .fill(
      [
        "kind: Block",
        "name: e2e-sysctl",
        "spec:",
        "  title: E2E sysctl",
        "  resources:",
        "    - sysctl:",
        "        name: net.ipv4.ip_forward",
        '        value: "1"',
        "",
      ].join("\n"),
    );
  await page.getByRole("textbox", { name: "Reason" }).fill("e2e: a VM-only Block");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await approveAndRun(page);

  await page.goto("/#/targets/new");
  await expect(page.getByRole("heading", { name: "New target" })).toBeVisible();
  const choice = page.locator("#add-choice option");
  await page.getByLabel("Image").selectOption({ value: "e2e-ct" });
  await expect(page.locator("#t-type")).toHaveText("Container (LXC)");
  await expect(page.locator("#blocks-hidden")).toContainText("e2e-sysctl");
  await expect(choice.filter({ hasText: "E2E sysctl" })).toHaveCount(0);
  await expect(choice.filter({ hasText: "Message of the day" })).toHaveCount(1);

  await page.getByLabel("Image").selectOption({ value: "ubuntu-26.04" });
  await expect(page.locator("#blocks-hidden")).toHaveCount(0);
  await expect(choice.filter({ hasText: "E2E sysctl" })).toHaveCount(1);
});

import { expect, test } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-096 on the real API: the Blocks screen. A browser check at the line,
// a server check (422) at the line, then a normal Plan that lists every
// Target that uses the Block.
test("blocks: edit motd as YAML, checked in the browser and the server", async ({ page }) => {
  await login(page);
  await page.getByRole("link", { name: "Blocks", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Blocks" })).toBeVisible();
  await page.getByRole("link", { name: "motd", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Block motd" })).toBeVisible();

  // The Targets that use motd.
  const users = (
    await page
      .locator("section", { has: page.getByRole("heading", { name: "Used by" }) })
      .locator("li:has-text('Target') a")
      .allTextContents()
  ).sort();
  expect(users.length).toBeGreaterThan(0);

  await page.getByRole("link", { name: "Edit", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Edit block motd" })).toBeVisible();
  const editor = page.getByRole("textbox", { name: "Block (YAML)" });
  const original = await editor.inputValue();
  expect(original).toContain("name: motd");

  // Browser check: an unknown key shows at its line, nothing is sent.
  await editor.fill(original.replace(/^spec:$/m, "spec:\n  colour: red"));
  await page.getByRole("textbox", { name: "Reason" }).fill("e2e: blocks screen");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("button", { name: "Line 4" })).toBeVisible();
  await expect(page.locator("#b-errors")).toContainText('Unknown key "colour"');
  await expect(editor).toHaveAttribute("aria-invalid", "true");
  await expect(page).toHaveURL(/#\/blocks\/motd\/edit$/);

  // Server check: an unquoted mode is a number in YAML 1.2; block.json
  // allows it, the resource schema of the server does not.
  const modeLine = original.split("\n").findIndex((l) => /^\s+mode: /.test(l)) + 1;
  expect(modeLine).toBeGreaterThan(0);
  const unquoted = original.replace(/mode: "?0644"?/, "mode: 0640");
  expect(unquoted).not.toBe(original);
  await editor.fill(unquoted);
  await expect(page.locator("#b-errors")).toContainText("The text matches the Block schema.");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("button", { name: `Line ${modeLine}` })).toBeVisible();
  // The error comes from the render of each Target that uses motd.
  await expect(page.locator("#b-errors")).toContainText("mode: got number, want string");
  for (const t of users) await expect(page.locator("#b-errors")).toContainText(`Target ${t}`);
  await expect(page.getByRole("alert")).not.toContainText("/spec/blocks");

  // A valid change: the Plan lists every Target that uses motd.
  await editor.fill(original.replace(/mode: "?0644"?/, 'mode: "0640"'));
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  const planned = (
    await page.getByRole("heading", { level: 2, name: /^Target / }).allTextContents()
  )
    .map((t) => t.replace(/^Target\s+/, "").trim())
    .sort();
  expect(planned).toEqual(users);
  await approveAndRun(page);

  // The new revision is on the Block page.
  await page.goto("/#/blocks/motd");
  await expect(page.getByRole("region", { name: "YAML of motd, scrolls" })).toContainText(
    'mode: "0640"',
  );
});

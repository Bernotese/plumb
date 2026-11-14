import { expect, test } from "@playwright/test";
import { login } from "./login";

// T-176 on the real API (T-174, D-361): Settings → General. The backup
// schedule shows where each value comes from; a change here wins over the
// install value until "Use the install value again". The Proxmox view is
// read-only (serve --dev has no Proxmox). The schedule is put back at the end.

test("settings general: change the backup schedule and go back to the install value", async ({
  page,
}) => {
  await login(page);
  await page.getByRole("link", { name: "Settings" }).click();
  await page.getByRole("link", { name: "General", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Backup schedule" })).toBeVisible();
  const before = (await (await page.request.get("/api/v1/settings")).json()) as {
    backup: { time: string; keep: number; source: { time: string; keep: string } };
  };
  await expect(page.locator("#g-time")).toHaveValue(before.backup.time);
  await expect(page.locator("#g-time-reset")).toHaveCount(0);

  // The browser refuses a wrong value; nothing is sent.
  await page.locator("#g-time").fill("24:00");
  await page.getByRole("button", { name: "Save schedule" }).click();
  await expect(page.locator("#g-time-error")).toContainText("HH:MM");

  const time = before.backup.time === "02:30" ? "02:45" : "02:30";
  await page.locator("#g-time").fill(time);
  await page.locator("#g-keep").fill("12");
  await page.getByRole("button", { name: "Save schedule" }).click();
  await expect(page.locator("#g-saved")).toContainText(
    `Saved. The next backup runs at ${time} UTC; Plumb keeps 12.`,
  );
  await expect(page.locator("#g-time-hint")).toContainText("set here");
  const backups = (await (await page.request.get("/api/v1/backups")).json()) as {
    schedule: { time: string; keep: number };
  };
  expect(backups.schedule).toEqual({ time, keep: 12 });
  await page.getByRole("link", { name: "Backups", exact: true }).click();
  await expect(page.getByText(`every day at ${time} UTC and keeps 12 backups`)).toBeVisible();

  // The server checks too: 422 names the field.
  const origin = new URL(page.url()).origin;
  const bad = await page.request.patch("/api/v1/settings", {
    headers: { origin },
    data: { backup: { keep: 0 } },
  });
  expect(bad.status()).toBe(422);
  expect(JSON.stringify(await bad.json())).toContain("/backup/keep");

  // Back to the install values.
  await page.getByRole("link", { name: "Change the schedule" }).click();
  await page.getByRole("button", { name: "Use the install value again" }).first().click();
  await expect(page.locator("#g-time")).toHaveValue(before.backup.time);
  await page.getByRole("button", { name: "Use the install value again" }).click();
  await expect(page.locator("#g-keep")).toHaveValue(String(before.backup.keep));
  await expect(page.locator("#g-time-reset")).toHaveCount(0);
  const after = (await (await page.request.get("/api/v1/settings")).json()) as typeof before;
  expect(after.backup).toEqual(before.backup);

  // serve --dev has no Proxmox: the view says so and shows no secret.
  await expect(page.locator("#general-proxmox")).toBeVisible();
  await expect(page.locator("#general-proxmox")).not.toContainText("secret:");
});

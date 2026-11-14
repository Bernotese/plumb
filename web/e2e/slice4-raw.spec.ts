import { expect, test } from "@playwright/test";
import { approveAndRun, login } from "./login";
// @ts-expect-error: plain ES module without types
import { startPlumb } from "./server.mjs";

// T-118 raw mode on the real API (T-125; D-296, D-301). Its own server:
// slice.spec.ts reverts the seeded motd drift of web2 on the shared one.
// Accept as raw makes an override; the Target page lists it with its
// Block, edits its content, and goes back to the Block. The motd template
// gets a function first, so no parameter fits (D-362).
test("raw mode: accept as raw, edit the raw content, back to the Block", async ({ browser }) => {
  test.setTimeout(180_000);
  const server = await startPlumb(0);
  try {
    const ctx = await browser.newContext({ baseURL: server.base });
    const page = await ctx.newPage();
    await login(page, server);

    // Accept maps a plain {{ .message }} back to the parameter (D-362), so
    // this spec first makes the template one that has no inverse (a
    // function): then the seeded hand edit really is raw.
    const origin = server.base;
    const motd = (await (await page.request.get("/api/v1/blocks/motd")).json()) as {
      document: {
        kind: string;
        name: string;
        spec: { resources: { file: { content: string } }[] };
      };
    };
    motd.document.spec.resources[0].file.content = "{{ .message | trim }}\n";
    const put = await page.request.post("/api/v1/plans", {
      headers: { origin },
      data: { reason: "e2e: motd template with a function", put: [motd.document] },
    });
    expect(put.status()).toBe(201);
    const ok = await page.request.post(`/api/v1/plans/${(await put.json()).id}/approve`, {
      headers: { origin },
      data: {},
    });
    expect(ok.status()).toBe(202);

    // The seeded drift: someone edited the motd of web2 by hand.
    await page.goto("/#/issues");
    const issueRow = page.getByRole("row", { name: /web2.*file:\/etc\/motd/ });
    await issueRow.getByRole("link", { name: /^Issue \d+$/ }).click();
    await expect(page.getByText("Status: open")).toBeVisible();
    await expect(page.locator("#raw-help")).toContainText("The Block motd does not set");
    await page.getByLabel("Reason (optional)").fill("e2e: keep the hand edit");
    await page.getByRole("button", { name: "Accept as raw for this Target" }).click();
    await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
    await approveAndRun(page);

    // The Target page lists the raw resource with its Block.
    await page.goto("/#/targets/web2/edit");
    const card = page.getByRole("region", { name: "Raw resources" });
    await expect(card.getByRole("heading", { name: "file:/etc/motd" })).toBeVisible();
    await expect(card.locator("#raw-0-source")).toHaveText("From the Block motd.");
    await expect(card.locator("#raw-0-value")).toHaveText("Edited by hand");

    // Edit the raw content: a reason is required, then a normal Plan.
    await card.getByRole("button", { name: "Edit content" }).click();
    await expect(card.locator("#raw-0-content")).toBeFocused();
    await card.locator("#raw-0-content").fill("Edited in Plumb\n");
    await card.getByRole("button", { name: "Show the plan" }).click();
    await expect(card.getByText("Say why you make this change.")).toBeVisible();
    await expect(card.locator("#raw-reason")).toBeFocused();
    await card.locator("#raw-reason").fill("e2e: new raw motd");
    await card.getByRole("button", { name: "Show the plan" }).click();
    await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
    await expect(page.getByText("file:/etc/motd").first()).toBeVisible();
    await approveAndRun(page);
    await page.goto("/#/targets/web2/edit");
    await expect(card.locator("#raw-0-value")).toHaveText("Edited in Plumb");

    // Back to the Block: the override goes, the Block sets the motd again.
    await card.locator("#raw-reason").fill("e2e: use the Block again");
    await card.getByRole("button", { name: "Back to the Block" }).click();
    await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
    await approveAndRun(page);
    await page.goto("/#/targets/web2/edit");
    await expect(card.locator("#raw-none")).toHaveText("This Target has no raw resources.");
    await ctx.close();
  } finally {
    await server.stop();
  }
});

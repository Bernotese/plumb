import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-196 on the real API (D-376): the Target form writes only what the
// person set. A Block default change reaches a Target that never chose a
// value; a value stored equal to the default stays until the person
// chooses "Follow the Block default". app1 and the Block are put back at
// the end, so later specs see the demo state.

type Doc = { kind: string; name: string; spec: Record<string, unknown> };
type Use = { use: string; params?: Record<string, unknown> };

const block = (def: string): Doc => ({
  kind: "Block",
  name: "e2e-defaults",
  spec: {
    title: "e2e defaults",
    params: {
      type: "object",
      properties: { msg: { type: "string", title: "Message", default: def } },
    },
    resources: [{ file: { path: "/etc/e2e-defaults", content: "{{ .msg }}\n" } }],
  },
});

async function plan(page: Page, body: Record<string, unknown>) {
  const origin = new URL(page.url()).origin;
  const res = await page.request.post("/api/v1/plans", { headers: { origin }, data: body });
  expect(res.status()).toBe(201);
  return (await res.json()) as { id: number; targets: { target: string }[] };
}

async function approve(page: Page, id: number) {
  const origin = new URL(page.url()).origin;
  const res = await page.request.post(`/api/v1/plans/${id}/approve`, {
    headers: { origin },
    data: {},
  });
  expect(res.status()).toBe(202);
}

async function discard(page: Page, id: number) {
  const origin = new URL(page.url()).origin;
  expect((await page.request.delete(`/api/v1/plans/${id}`, { headers: { origin } })).status()).toBe(
    204,
  );
}

async function app1(request: APIRequestContext): Promise<Doc> {
  return ((await (await request.get("/api/v1/targets/app1")).json()) as { document: Doc }).document;
}

test("Block defaults: a new default reaches the Target; a fixed value stays", async ({ page }) => {
  await login(page);
  const original = await app1(page.request);
  await approve(
    page,
    (await plan(page, { reason: "e2e: defaults block", put: [block("one")] })).id,
  );

  // The form adds the Block and writes no parameter.
  await page.goto("/#/targets/app1/edit");
  await page.locator("#add-choice").selectOption("e2e-defaults");
  await page.getByRole("button", { name: "Add Block" }).click();
  await expect(page.getByLabel("Message")).toHaveValue("one");
  await page.locator("#t-reason").fill("e2e: use the defaults block");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await approveAndRun(page);
  const saved = await app1(page.request);
  const use = (saved.spec["blocks"] as Use[]).find((u) => u.use === "e2e-defaults");
  expect(use).toEqual({ use: "e2e-defaults" });
  expect(saved.spec["overrides"]).toEqual(original.spec["overrides"]);

  // A new default reaches app1.
  let p = await plan(page, { reason: "e2e: new default", put: [block("two")] });
  expect(p.targets.map((t) => t.target)).toContain("app1");
  await discard(page, p.id);

  // app1 fixes the value to the current default: the form says so, and a
  // new default does not reach it.
  const fixed = structuredClone(saved);
  for (const u of fixed.spec["blocks"] as Use[])
    if (u.use === "e2e-defaults") u.params = { msg: "one" };
  await approve(page, (await plan(page, { reason: "e2e: fix the value", put: [fixed] })).id);
  p = await plan(page, { reason: "e2e: new default", put: [block("two")] });
  expect(p.targets.map((t) => t.target)).not.toContain("app1");
  await discard(page, p.id);

  await page.goto("/#/targets/app1/edit");
  await expect(page.getByText("Fixed to the current default")).toBeVisible();
  await page.getByRole("button", { name: "Follow the Block default" }).click();
  await expect(page.getByRole("button", { name: "Follow the Block default" })).toHaveCount(0);
  await page.locator("#t-reason").fill("e2e: follow the default");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(
    page
      .getByRole("region", { name: "YAML diff of Target app1" })
      .locator("tr.del", { hasText: "msg: one" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Drop plan" }).click();
  await page.locator("#drop-confirm").click();
  await expect(page.getByText("This plan is stale.")).toBeVisible();

  // Put app1 back, then remove the Block.
  await approve(page, (await plan(page, { reason: "e2e: back", put: [original] })).id);
  await approve(
    page,
    (
      await plan(page, {
        reason: "e2e: remove the defaults block",
        delete: [{ kind: "Block", name: "e2e-defaults" }],
      })
    ).id,
  );
});

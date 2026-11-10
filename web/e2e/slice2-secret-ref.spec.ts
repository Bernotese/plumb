import { expect, test, type Page } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-036 on the real API (T-029): the secret picker stores a reference, and
// a new binding is human-only and unverified until the apply (D-156).
const ORIGIN = { origin: "http://127.0.0.1:18460" };

/** Puts a Block like blocks/app-env.yaml (a writeOnly parameter) through an approved Plan. */
async function addAppEnvBlock(page: Page): Promise<void> {
  const spec = {
    title: "Application environment file",
    params: {
      type: "object",
      required: ["password"],
      properties: {
        path: { type: "string", title: "File path", default: "/etc/app.env" },
        user: { type: "string", title: "Database user", default: "app" },
        password: { type: "string", title: "Database password (a secret)", writeOnly: true },
      },
    },
    resources: [
      {
        file: {
          path: "{{ .path }}",
          content: "APP_USER={{ .user }}\nAPP_PASSWORD={{ .password }}\n",
          mode: "0600",
          owner: "root",
          group: "root",
        },
      },
    ],
  };
  const res = await page.request.post("/api/v1/plans", {
    headers: ORIGIN,
    data: { reason: "e2e: app-env block", put: [{ kind: "Block", name: "app-env", spec }] },
  });
  expect(res.ok()).toBe(true);
  const plan = (await res.json()) as { id: number };
  const ok = await page.request.post(`/api/v1/plans/${plan.id}/approve`, {
    headers: ORIGIN,
    data: {},
  });
  expect(ok.status()).toBeLessThan(300);
}

test("secret picker: a new binding is human-only and unverified", async ({ page }) => {
  const value = "e2e-db-value-3c9a";
  await login(page);
  await addAppEnvBlock(page);

  await page.getByRole("link", { name: "web1", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Edit target web1" })).toBeVisible();
  await page.getByLabel("Block to add").selectOption({ label: "Application environment file" });
  await page.getByRole("button", { name: "Add Block" }).click();
  await page.getByRole("button", { name: "New secret" }).click();
  await page.getByLabel("Secret name").fill("e2e-db");
  await page.getByLabel("Secret value").fill(value);
  await page.getByRole("button", { name: "Create secret" }).click();
  await expect(page.getByLabel("Secret name")).toHaveCount(0);
  await expect(page.getByLabel("Database password (a secret)")).toHaveValue("e2e-db");
  await page.getByRole("textbox", { name: "Reason", exact: true }).fill("e2e: app env");
  await page.getByRole("button", { name: "Show the plan" }).click();

  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  const planId = Number(/\d+$/.exec(page.url())?.[0]);
  await expect(page.getByText(/Uses secrets:\s*e2e-db\s*\(version \d+\)/)).toBeVisible();
  await expect(page.getByText("Only a person can approve this plan.")).toBeVisible();
  await expect(
    page.getByText("It gives a secret to web1 that web1 did not get before."),
  ).toBeVisible();
  const res = page.getByRole("region", { name: /file:\/etc\/app\.env/ });
  await expect(res).toContainText("Create");
  await expect(res).toContainText("changed (hidden)");
  await expect(res).toContainText("Not observed before the approval");
  await expect(
    page.getByText(/not observed before the approval: they get a new secret.*\(D-156/),
  ).toBeVisible();
  expect(await page.content()).not.toContain(value);

  // The target stores a reference, never the value (D-088).
  const t = await page.request.get("/api/v1/plans/" + planId);
  expect(await t.text()).not.toContain(value);

  // An apply token with enough risk still cannot approve it (D-099).
  const tok = await page.request.post("/api/v1/tokens", {
    headers: ORIGIN,
    data: { name: "e2e approver", tier: "apply", max_risk: "disruptive" },
  });
  expect(tok.status()).toBe(201);
  const { bearer } = (await tok.json()) as { bearer: string };
  const denied = await page.request.post(`/api/v1/plans/${planId}/approve`, {
    headers: { authorization: `Bearer ${bearer}` },
    data: {},
  });
  expect(denied.status()).toBe(403);
  expect(((await denied.json()) as { type: string }).type).toMatch(/human-required$/);

  // A person approves it; the agent observes and applies the new file.
  await approveAndRun(page);
  await expect(page.getByRole("row", { name: /file:\/etc\/app\.env/ })).toContainText("applied");
  const doc = await page.request.get("/api/v1/targets/web1");
  const body = await doc.text();
  expect(body).not.toContain(value);
  expect(body).toContain('"password":{"secret":"e2e-db"}');
});

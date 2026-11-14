import { expect, test, type Page } from "@playwright/test";
import { login } from "./login";

// T-177 on the real API (D-363): the YAML mode of the Target and Image
// forms and nested parameter forms. A YAML edit is the same proposal as the
// form; the server checks it. Every plan here is dropped, so later specs
// see the demo state unchanged.

async function drop(page: Page) {
  await page.getByRole("button", { name: "Drop plan" }).click();
  await page.locator("#drop-confirm").click();
  await expect(page.getByText("This plan is stale.")).toBeVisible();
}

test("target YAML mode: an edit makes a plan; a server error shows at its line", async ({
  page,
}) => {
  await login(page);
  await page.goto("/#/targets/app1/edit");
  await page.getByRole("button", { name: "YAML", exact: true }).click();
  const editor = page.locator("#t-yaml");
  await expect(editor).toBeVisible();
  const text = await editor.inputValue();
  expect(text).toMatch(/^kind: Target\nname: app1\nspec:\n/);
  const memory = Number(/memory: (\d+)/.exec(text)?.[1]);

  // A value the server refuses: its 422 pointer shows at the line.
  await editor.fill(text.replace(/cores: \d+/, "cores: 0"));
  await page.locator("#t-reason").fill("e2e: YAML mode");
  await page.getByRole("button", { name: "Show the plan" }).click();
  const errors = page.locator("#t-yaml-errors");
  await expect(errors).toContainText("(server)");
  const line = text.split("\n").findIndex((l) => /^\s+cores: /.test(l)) + 1;
  await expect(errors.getByRole("button", { name: `Line ${line}` })).toBeVisible();

  await editor.fill(text.replace(`memory: ${memory}`, `memory: ${memory * 2}`));
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.locator("#docs-count")).toContainText("1 changed");
  await expect(
    page
      .getByRole("region", { name: "YAML diff of Target app1" })
      .locator("tr.add", { hasText: `memory: ${memory * 2}` }),
  ).toBeVisible();
  await drop(page);
});

test("target YAML mode: problems stay in YAML; YAML back to the form", async ({ page }) => {
  await login(page);
  await page.goto("/#/targets/app1/edit");
  await page.getByRole("button", { name: "YAML", exact: true }).click();
  const editor = page.locator("#t-yaml");
  const text = await editor.inputValue();
  await editor.fill(text.replace("name: app1", "name: app2"));
  await page.getByRole("button", { name: "Form", exact: true }).click();
  await expect(page.locator("#t-mode-error")).toContainText("Fix them to switch back");
  await expect(page.locator("#t-yaml-errors")).toContainText('Line 2: The name must stay "app1".');
  await editor.fill(text.replace(/cores: \d+/, "cores: 3"));
  await page.getByRole("button", { name: "Form", exact: true }).click();
  await expect(page.locator("#guest-cores")).toHaveValue("3");
});

test("nested parameter form: a Block with object parameters", async ({ page }) => {
  await login(page);
  const origin = new URL(page.url()).origin;
  const block = {
    kind: "Block",
    name: "e2e-nested",
    spec: {
      title: "e2e nested",
      params: {
        type: "object",
        required: ["site"],
        properties: {
          site: {
            type: "object",
            title: "Site",
            required: ["name"],
            properties: { name: { type: "string", title: "Name" } },
          },
          upstreams: {
            type: "array",
            title: "Upstream",
            items: {
              type: "object",
              required: ["host"],
              properties: {
                host: { type: "string", title: "Host" },
                port: { type: "integer", title: "Port", default: 80 },
              },
            },
          },
        },
      },
      resources: [{ file: { path: "/etc/e2e-nested", content: "{{ .site.name }}\n" } }],
    },
  };
  const made = await page.request.post("/api/v1/plans", {
    headers: { origin },
    data: { reason: "e2e: nested block", put: [block] },
  });
  expect(made.status()).toBe(201);
  const id = ((await made.json()) as { id: number }).id;
  expect(
    (
      await page.request.post(`/api/v1/plans/${id}/approve`, { headers: { origin }, data: {} })
    ).status(),
  ).toBe(202);

  await page.goto("/#/targets/app1/edit");
  await page.locator("#add-choice").selectOption("e2e-nested");
  await page.getByRole("button", { name: "Add Block" }).click();
  const use = page.locator("fieldset", { has: page.getByText("e2e nested", { exact: true }) });
  await use.getByLabel("Name").fill("shop");
  await use.getByRole("button", { name: "Add upstream" }).click();
  await use.getByRole("button", { name: "Add upstream" }).click();
  await use.getByLabel("Host").nth(0).fill("a.lan");
  await use.getByLabel("Host").nth(1).fill("b.lan");
  await use.getByRole("button", { name: "Move Upstream 2 up" }).click();
  await page.locator("#t-reason").fill("e2e: nested params");
  // The YAML view shows the nested parameters the form saves.
  await page.getByRole("button", { name: "YAML", exact: true }).click();
  await expect(page.locator("#t-yaml")).toHaveValue(
    /- use: e2e-nested\n {6}params:\n {8}site:\n {10}name: shop\n {8}upstreams:\n {10}- host: b\.lan\n {12}port: 80\n {10}- host: a\.lan\n {12}port: 80\n/,
  );
  await page.getByRole("button", { name: "Form", exact: true }).click();
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: /file:\/etc\/e2e-nested/ })).toBeVisible();
  await drop(page);
});

test("image YAML mode: an edit makes a plan", async ({ page }) => {
  await login(page);
  await page.goto("/#/images/ubuntu-26.04/edit");
  await page.getByRole("button", { name: "YAML", exact: true }).click();
  const editor = page.locator("#i-yaml");
  const text = await editor.inputValue();
  expect(text).toMatch(/^kind: Image\nname: ubuntu-26\.04\nspec:\n/);
  await editor.fill(text.replace(/title: .*/, "title: Ubuntu from YAML"));
  await page.locator("#i-reason").fill("e2e: image YAML");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
  await expect(
    page
      .getByRole("region", { name: "YAML diff of Image ubuntu-26.04" })
      .locator("tr.add", { hasText: "title: Ubuntu from YAML" }),
  ).toBeVisible();
  await drop(page);
});

import { readFile } from "node:fs/promises";
import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { approveAndRun, login } from "./login";
// @ts-expect-error: plain ES module without types
import { startPlumb } from "./server.mjs";

// T-035 export and import on the real API (T-033): export without and with
// the encrypted secret bundle on the shared server, import into a fresh
// server, a refused import (409 secret-conflict) changes nothing, and an
// explicit overwrite.
const A = "http://127.0.0.1:18460";
const PASS = "e2e transfer passphrase";
const V1 = "e2e-transfer-value-one-5d2e";
const V2 = "e2e-transfer-value-two-8a41";

async function putSecret(req: APIRequestContext, base: string, value: string) {
  const res = await req.put(`${base}/api/v1/secrets/e2e-transfer`, {
    headers: { origin: base },
    data: { value },
  });
  expect(res.ok()).toBe(true);
}

/** The server state that an import must not change before an approval. */
async function snapshot(req: APIRequestContext, base: string) {
  const json = async (path: string) => {
    const res = await req.get(`${base}/api/v1${path}`);
    expect(res.ok()).toBe(true);
    return (await res.json()) as { items: Record<string, unknown>[] };
  };
  const secrets = (await json("/secrets")).items.map(
    (s) => `${String(s["name"])}@${String(s["version"])}`,
  );
  const plans = (await json("/plans?limit=200")).items.length;
  const head = (await json("/revisions?limit=1")).items[0]?.["id"];
  return { secrets, plans, head };
}

async function download(page: Page): Promise<string> {
  const dl = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download export" }).click();
  const file = await dl;
  return readFile(await file.path(), "utf8");
}

/** The documents of an export, without its header. */
const docs = (yaml: string) =>
  yaml
    .split(/^---$/m)
    .filter((d) => !/kind: Export\b/.test(d))
    .join("---");

async function importFile(page: Page, text: string, reason: string) {
  await page.getByLabel("Export file").setInputFiles({
    name: "plumb-export.yaml",
    mimeType: "application/yaml",
    buffer: Buffer.from(text),
  });
  await page.getByLabel("Passphrase of the secret bundle (optional)").fill(PASS);
  await page.getByLabel("Reason (required)").fill(reason);
}

test("export with and without secrets, import into a fresh server", async ({ page, browser }) => {
  // Export on the shared server.
  await login(page);
  await putSecret(page.request, A, V1);
  await page.goto("/#/settings/export");
  await expect(page.getByRole("heading", { name: "Export" })).toBeVisible();

  const plain = await download(page);
  await expect(page.getByRole("status")).toContainText(/plumb-export-.*\.yaml/);
  expect(plain).toMatch(/kind: Export/);
  expect(plain).toContain("e2e-transfer");
  expect(plain).not.toContain("SecretBundle");
  expect(plain).not.toContain(V1);

  await page.getByLabel(/With secret values, encrypted/).check();
  await page.getByLabel("Passphrase", { exact: true }).fill(PASS);
  await page.getByLabel("Passphrase again").fill(PASS);
  const bundle = await download(page);
  await expect(page.getByRole("status")).toContainText(/with-secrets\.yaml/);
  expect(bundle).toContain("kind: SecretBundle");
  expect(bundle).not.toContain(V1);
  await expect(page.getByLabel("Passphrase", { exact: true })).toHaveValue("");

  // A fresh server in its own browser context (its own session cookie).
  const fresh = (await startPlumb(0, { demo: false })) as {
    base: string;
    password: string;
    stop: () => Promise<void>;
  };
  const B = fresh.base;
  const ctx = await browser.newContext({ baseURL: B });
  try {
    const pb = await ctx.newPage();
    await login(pb, { base: `${B}/`, password: fresh.password });
    const before = await snapshot(pb.request, B);

    // The import shows a Plan first: no revision yet.
    await pb.goto(`${B}/#/settings/export`);
    await importFile(pb, bundle, "e2e: import from the shared server");
    await pb.getByRole("button", { name: "Make import plan" }).click();
    await expect(pb.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
    await expect(pb.getByText("e2e-transfer").first()).toBeVisible();
    const afterPlan = await snapshot(pb.request, B);
    expect(afterPlan.head).toEqual(before.head);
    expect(afterPlan.secrets.some((s) => s.startsWith("e2e-transfer@"))).toBe(true);

    // Another value on the fresh server: the import is refused and changes nothing.
    await putSecret(pb.request, B, V2);
    const refusedBefore = await snapshot(pb.request, B);
    await pb.goto(`${B}/#/settings/export`);
    await importFile(pb, bundle, "e2e: import again");
    await pb.getByRole("button", { name: "Make import plan" }).click();
    const box = pb.getByRole("group", { name: "Secrets with another value" });
    await expect(box).toBeVisible();
    const tick = box.getByLabel("Overwrite e2e-transfer");
    await expect(tick).not.toBeChecked();
    expect(await snapshot(pb.request, B)).toEqual(refusedBefore);

    // An explicit tick overwrites only that secret.
    await tick.check();
    await pb.getByRole("button", { name: "Import again" }).click();
    await expect(pb.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
    await expect(pb.getByText(/e2e-transfer/).first()).toBeVisible();
    const overwritten = await snapshot(pb.request, B);
    expect(overwritten.secrets).not.toEqual(refusedBefore.secrets);
    expect(overwritten.head).toEqual(before.head);

    // After the approval the fresh server holds the documents of the export.
    await approveAndRun(pb);
    const res = await pb.request.get(`${B}/api/v1/export`);
    expect(res.ok()).toBe(true);
    expect(docs(await res.text())).toEqual(docs(plain));
  } finally {
    await ctx.close();
    await fresh.stop();
  }
});

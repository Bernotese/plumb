import { expect, test, type Page } from "@playwright/test";
import { approveAndRun, login } from "./login";

// T-239 (rest) on the real API with the fake driver (serve --dev): the
// data disk list of the Target form against what T-238 does (ARCHITECTURE
// §21, D-436, D-437). The Targets are made here, so no earlier spec has
// planned them; the container uses the Image e2e-ct of slice2-lxc.spec.ts.

let ORIGIN: { origin: string };

/** Proposes documents through the API as the admin session, approves, waits. */
async function applyDocs(page: Page, put: object[]): Promise<void> {
  const plan = await page.request.post("/api/v1/plans", {
    headers: ORIGIN,
    data: { reason: "e2e: slice 8 disks", put },
  });
  expect(plan.status(), await plan.text()).toBe(201);
  const { id } = (await plan.json()) as { id: number };
  const ok = await page.request.post(`/api/v1/plans/${id}/approve`, { headers: ORIGIN, data: {} });
  expect(ok.status(), await ok.text()).toBe(202);
  await expect
    .poll(
      async () => {
        const p = (await (await page.request.get(`/api/v1/plans/${id}`)).json()) as {
          run_id: number | null;
        };
        if (!p.run_id) return "";
        return (
          (await (await page.request.get(`/api/v1/runs/${p.run_id}`)).json()) as { status: string }
        ).status;
      },
      { timeout: 45_000 },
    )
    .toBe("succeeded");
}

/** The row of a guest:config change in the open Plan view. */
const change = (page: Page, attr: string) =>
  page.getByRole("row", { name: new RegExp(`^${attr}\\b`) });

async function showPlan(page: Page, reason: string): Promise<void> {
  await page.getByLabel("Reason").fill(reason);
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  await login(page);
  ORIGIN = { origin: new URL(page.url()).origin };
});

test("VM data disks: add, grow only, remove by a person, unmanage", async ({ page }) => {
  // A VM Target like web1, under its own name.
  const web1 = (await (await page.request.get("/api/v1/targets/web1")).json()) as {
    document: { spec: Record<string, unknown> };
  };
  await applyDocs(page, [{ kind: "Target", name: "e2e8-vm", spec: web1.document.spec }]);

  // (1) Add: the first-time note, a live hot-add, reversible.
  await page.goto("/#/targets/e2e8-vm/edit");
  await expect(page.locator("#disks-hint")).toContainText("A VM gets a new disk live (reversible)");
  await expect(page.locator("#disks-hint")).toContainText("lsblk -o NAME,SERIAL");
  await page.getByLabel("Plumb manages the data disks of this guest").check();
  await expect(page.locator("#disks-unmanaged-vm")).toContainText("has no serial plumb-N");
  await page.locator("#add-disk").click();
  await page.locator("#disk-1-size").fill("20");
  await showPlan(page, "e2e: add a data disk");
  await expect(change(page, "disk1")).toContainText("20 GiB");
  await expect(change(page, "disk1")).toContainText("reversible");
  await approveAndRun(page);
  await page.goto("/#/targets/e2e8-vm/edit");
  await expect(page.locator("#disk-1-slot")).toHaveText("1 (scsi1)");

  // (2) Grow only: smaller is a field error; bigger is a disruptive grow.
  await page.locator("#disk-1-size").fill("10");
  await page.getByLabel("Reason").fill("e2e: shrink");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.locator("#disk-1-size-error")).toContainText(
    "A disk only grows: at least 20 GiB",
  );
  await page.locator("#disk-1-size").fill("30");
  await showPlan(page, "e2e: grow the data disk");
  await expect(change(page, "disk1")).toContainText("20 GiB");
  await expect(change(page, "disk1")).toContainText("30 GiB");
  await expect(change(page, "disk1")).toContainText("disruptive");

  // (3) Remove: the banner, allow_restart for a running guest, destructive,
  // the warning names the volume; a token cannot approve, a person can.
  await page.goto("/#/targets/e2e8-vm/edit");
  await page.getByRole("button", { name: "Remove the disk in slot 1" }).click();
  await expect(page.locator("#disks-removed")).toContainText(
    "Plumb detaches the disk first, then frees its volume in a second step",
  );
  // The disk checks run at the submit: a running guest needs allow_restart.
  await page.getByLabel("Reason").fill("e2e: remove the data disk");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.locator("#disks-remove-error")).toContainText(
    "Removing a disk of a running guest needs",
  );
  await page.locator("#t-allow-restart").check();
  await showPlan(page, "e2e: remove the data disk");
  await expect(change(page, "disk1")).toContainText("destructive");
  await expect(
    page.getByText(/The data on disk 1 \(fake:vm-\d+-disk-1, 20 GiB\) is lost\./),
  ).toBeVisible();
  const planId = /Plan (\d+)/.exec(
    (await page.getByRole("heading", { name: /^Plan \d+$/ }).textContent()) ?? "",
  )?.[1];
  const tok = await page.request.post("/api/v1/tokens", {
    headers: ORIGIN,
    data: { name: "e2e disk approver", tier: "apply", max_risk: "disruptive" },
  });
  expect(tok.status()).toBe(201);
  const { bearer } = (await tok.json()) as { bearer: string };
  const denied = await page.request.post(`/api/v1/plans/${planId}/approve`, {
    headers: { authorization: `Bearer ${bearer}` },
    data: {},
  });
  expect(denied.status()).toBe(403);
  await approveAndRun(page);

  // (5) Untick the box: the document has no disks, the Plan has no disk line.
  await page.goto("/#/targets/e2e8-vm/edit");
  await page.getByLabel("Plumb manages the data disks of this guest").uncheck();
  await expect(page.locator("#disks-managed-hint")).toContainText(
    "Plumb does not touch data disks",
  );
  await showPlan(page, "e2e: stop managing data disks");
  await expect(page.getByRole("row", { name: /^disk[1-3]\b/ })).toHaveCount(0);
  // Only the document changes (no guest change): approve it by the API and
  // read the stored document.
  const unmanage = /Plan (\d+)/.exec(
    (await page.getByRole("heading", { name: /^Plan \d+$/ }).textContent()) ?? "",
  )?.[1];
  const ok = await page.request.post(`/api/v1/plans/${unmanage}/approve`, {
    headers: ORIGIN,
    data: {},
  });
  expect(ok.status(), await ok.text()).toBe(202);
  await expect
    .poll(async () => {
      const doc = (await (await page.request.get("/api/v1/targets/e2e8-vm")).json()) as {
        document: { spec: { guest: Record<string, unknown> } };
      };
      return "disks" in doc.document.spec.guest;
    })
    .toBe(false);
});

test("container data disks: path rules and the stop-add-start rule (D-437)", async ({ page }) => {
  await applyDocs(page, [
    {
      kind: "Target",
      name: "e2e8-ct",
      spec: {
        type: "lxc",
        image: "e2e-ct",
        guest: { cores: 1, memory: 512, disk: 4, bridge: "vmbr0", ip: "dhcp" },
      },
    },
  ]);
  await page.goto("/#/targets/e2e8-ct/edit");
  await expect(page.locator("#disks-hint")).toContainText(
    "added only while the container is stopped (D-437)",
  );
  await page.getByLabel("Plumb manages the data disks of this guest").check();
  await expect(page.locator("#disks-unmanaged-lxc")).toBeVisible();
  await page.locator("#add-disk").click();
  await page.locator("#disk-1-path").fill("/etc/x");
  await page.getByLabel("Reason").fill("e2e: container disk");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.locator("#disk-1-path-error")).toBeVisible();
  // A running container without allow_restart: the add stops it, so no.
  await page.locator("#disk-1-path").fill("/srv/data");
  await page.getByRole("button", { name: "Show the plan" }).click();
  await expect(page.locator("#disks-add-error")).toContainText(
    "Adding a container disk stops and starts the container",
  );
  await page.locator("#t-allow-restart").check();
  await showPlan(page, "e2e: container disk");
  await expect(change(page, "disk1")).toContainText("2 GiB at /srv/data");
  await expect(change(page, "disk1")).toContainText("disruptive");
  await expect(
    page.getByText(
      /the container is stopped, the disk is added, and the container is started again \(D-437\)/,
    ),
  ).toBeVisible();
});

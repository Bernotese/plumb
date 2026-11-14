import { expect, test } from "@playwright/test";
import { approveAndRun, login } from "./login";
// @ts-expect-error: plain ES module without types
import { startPlumb } from "./server.mjs";

// T-175 (D-362) on the real API, with its own server: the motd Block renders
// content from {{ .message }}, so Accept of the seeded hand edit on web2
// sets the Block parameter of web2, not an override (raw mode stays for
// templates without an inverse: slice4-raw.spec.ts). The button text is the
// one of today; T-176 names the parameter in it.
test("accept into a Block parameter: no override, the parameter holds the guest text", async ({
  browser,
}) => {
  test.setTimeout(180_000);
  const server = await startPlumb(0);
  try {
    const ctx = await browser.newContext({ baseURL: server.base });
    const page = await ctx.newPage();
    await login(page, server);

    await page.goto("/#/issues");
    const issueRow = page.getByRole("row", { name: /web2.*file:\/etc\/motd/ });
    await issueRow.getByRole("link", { name: /^Issue \d+$/ }).click();
    await expect(page.getByText("Status: open")).toBeVisible();
    const id = /#\/issues\/(\d+)/.exec(page.url())?.[1];
    const issue = (await (await page.request.get(`/api/v1/issues/${id}`)).json()) as {
      accept_mode: string;
      raw: boolean;
      accept_params: { use: string; param: string }[];
    };
    expect(issue.accept_mode).toBe("param");
    expect(issue.raw).toBe(false);
    expect(issue.accept_params).toEqual([{ use: "motd", index: 0, param: "message" }]);
    await expect(page.locator("#raw-help")).toHaveCount(0);

    await page.getByLabel("Reason (optional)").fill("e2e: keep the hand edit in the parameter");
    await page.getByRole("button", { name: /^Accept/ }).click();
    await expect(page.getByRole("heading", { name: /^Plan \d+$/ })).toBeVisible();
    await approveAndRun(page);

    // web2 holds the guest text in params.message; no override, no raw resource.
    const target = (await (await page.request.get("/api/v1/targets/web2")).json()) as {
      document: {
        spec: { blocks: { use: string; params?: Record<string, unknown> }[]; overrides?: object };
      };
      raw: unknown[];
    };
    const use = target.document.spec.blocks.find((b) => b.use === "motd");
    expect(use?.params?.["message"]).toBe("Edited by hand");
    expect(Object.keys(target.document.spec.overrides ?? {})).toHaveLength(0);
    expect(target.raw).toEqual([]);
    await page.goto(`/#/issues/${id}`);
    await expect(page.getByText("Status: resolved")).toBeVisible();
    await ctx.close();
  } finally {
    await server.stop();
  }
});

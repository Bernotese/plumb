import { defineConfig } from "@playwright/test";

// End-to-end test of the UI against `plumb serve --dev --dev-demo` (T-014).
// Run `make build` first: the test starts ../bin/plumb, which embeds web/dist.
export default defineConfig({
  testDir: "e2e",
  globalSetup: "./e2e/server.mjs",
  timeout: 90_000,
  expect: { timeout: 15_000 },
  retries: 0,
  workers: 1,
  use: {
    baseURL: "http://127.0.0.1:18460",
    trace: "retain-on-failure",
  },
  reporter: [["list"]],
});

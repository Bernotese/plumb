import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "mock-e2e",
  use: { baseURL: "http://127.0.0.1:18465" },
  webServer: {
    command: "npm run dev:mock -- --host 0.0.0.0 --port 18465 --strictPort",
    url: "http://127.0.0.1:18465",
    reuseExistingServer: false,
  },
});

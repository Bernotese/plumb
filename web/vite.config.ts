import { defineConfig } from "vitest/config";
import { loadEnv } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// The UI is served by `plumb` from the same origin, so API calls use relative URLs.
// In `vite dev`, /api is proxied to `plumb serve --dev` (PLUMB_DEV_URL, default :8443).
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "PLUMB_");
  return {
    plugins: [svelte()],
    build: {
      outDir: "dist",
      emptyOutDir: true,
      sourcemap: false,
    },
    server: {
      proxy: {
        "/api": env["PLUMB_DEV_URL"] ?? "http://127.0.0.1:8443",
      },
    },
    test: {
      include: ["src/**/*.test.ts"],
    },
  };
});

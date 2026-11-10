import { describe, expect, it } from "vitest";

// T-014: the UI never stores a password or a token in browser storage.
// Only the theme choice (theme.ts) uses localStorage.
const sources = import.meta.glob<string>(
  ["/src/**/*.ts", "/src/**/*.svelte", "!/src/**/*.test.ts", "!/src/lib/api.gen.ts"],
  {
    query: "?raw",
    import: "default",
    eager: true,
  },
);

describe("browser storage", () => {
  it("is used only by theme.ts", () => {
    expect(Object.keys(sources).length).toBeGreaterThan(20);
    const users = Object.entries(sources)
      .filter(([, text]) => /localStorage|sessionStorage|indexedDB|document\.cookie/.test(text))
      .map(([path]) => path);
    expect(users).toEqual(["/src/lib/theme.ts"]);
  });
});

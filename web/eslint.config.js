import js from "@eslint/js";
import ts from "typescript-eslint";
import svelte from "eslint-plugin-svelte";
import prettier from "eslint-config-prettier";
import globals from "globals";
import svelteConfig from "./svelte.config.js";

export default ts.config(
  { ignores: ["dist/", "dist-mock/", "test-results/", "playwright-report/", "node_modules/", "src/lib/api.gen.ts"] },
  js.configs.recommended,
  ...ts.configs.strict,
  ...svelte.configs.recommended,
  prettier,
  ...svelte.configs.prettier,
  {
    languageOptions: { globals: { ...globals.browser } },
  },
  {
    files: ["scripts/**", "*.config.{js,ts}"],
    languageOptions: { globals: { ...globals.node } },
  },
  {
    files: ["**/*.svelte", "**/*.svelte.ts"],
    languageOptions: {
      parserOptions: {
        projectService: true,
        extraFileExtensions: [".svelte"],
        parser: ts.parser,
        svelteConfig,
      },
    },
  },
);

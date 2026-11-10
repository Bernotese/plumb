# T-017 – OpenAPI contract and web skeleton

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer (pl-architect checks the contract)   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** user approval; merge after T-003   · **Parallel with:** T-003
- **Status:** review (PR open; Makefile lines wait for T-003)

## Goal
The public API contract for the slice exists, and the web app builds with generated API types.

## Context (read only these)
- `docs/ARCHITECTURE.md` §3, §4, §7, §9
- `docs/BRIEF.md` §5.2, §5.9
- Touch only `api/` and `web/`, and fill the Makefile stubs `build-web` and `lint-web` from T-003 (no other Makefile change)

## Acceptance criteria
- [ ] `api/openapi.yaml` (OpenAPI 3.1): session (login, logout, change password), images, blocks, targets (with status and `reenroll`), plans, runs, issues, events. Every response has an example.
- [ ] Errors are problem+json. Lists have `items` and `next`. Fields are `snake_case`. The security schemes describe the session cookie and the token tiers per endpoint.
- [ ] `web/`: Vite + Svelte + TypeScript skeleton with one page. `npm run gen` creates the API types from `api/openapi.yaml`. `web/dist` is embedded in `plumb`.
- [ ] Only the web dependencies of ARCHITECTURE §9 are used.
- [ ] `make build-web` builds the UI, so `make build` builds the UI and the binaries. `make lint-web` runs `eslint`, `prettier --check` and `svelte-check`.
- [ ] Tests: `make lint test build` passes. The OpenAPI file passes a schema check in CI.

## Out of scope
- Screens (T-006).
- Go handlers (T-011).

## Plan (architect, optional)

## Result (owner, max 8 lines)
- Branch `pl-frontend/T-017-openapi-web-skeleton`. `api/openapi.yaml` covers session (incl. password), images, blocks, targets (+status, reenroll), plans, runs, issues, events. Token tiers = 3.1 security roles (minimum tier).
- `npm run check:api`: Redocly `recommended-strict` (examples validated against schemas) + own checks: every response body has an example, all properties snake_case. Part of `npm run lint` → `make lint-web` → CI.
- `web/`: Svelte 5 + Vite + TS (5.9; openapi-typescript needs TS 5). One page (Targets). `src/lib/api.ts` typed GET client over generated `api.gen.ts` (not committed; `npm run gen`).
- Embed: Go package `web` (`web/embed.go`, `//go:embed all:dist`, `FS()`, `Built()`). `web/dist/.gitkeep` is force-added (root `.gitignore` has `dist/`). `cmd/plumb`/`internal/api` import it (T-011 serves it).
- Makefile lines for after T-003: `build-web: cd web && npm ci && npm run build` · `lint-web: cd web && npm ci && npm run lint` · `test` also runs `cd web && npm test` · Go targets use `go list ./... | grep -v /node_modules/` and `gofmt -l` skips `web/node_modules` (review should 1).
- Tests: web lint/test(8)/build pass; clean checkout `go vet ./... && go test ./web` passes with and without built UI. `make` not yet run (no Makefile on main).
- Review fixes (cd6f31b+1): reenroll restart as own `action: restart`, login Origin check, per-kind id params, list items = full objects, param snake_case check, `current_password` minLength. Open for pl-architect: schema openness/two sources, `ignore_rule`. Decision line needed for build deps outside §9 (`@redocly/openapi-core`, `@sveltejs/vite-plugin-svelte`, `@tsconfig/svelte`, `globals`).
- Not in contract (card scope): revisions, tokens, secrets, export/import. `npm audit`: dev-tool advisories only (deepmerge, minimatch), nothing in `dist`.

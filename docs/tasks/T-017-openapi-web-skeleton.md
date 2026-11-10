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
- Branch `pl-frontend/T-017-openapi-web-skeleton`, PR #2. Contract follows D-043…D-048: integer ids, principal strings, wire states, `Change` = `wire.Change`, document schemas by `$ref` to `internal/state/schema/*.json` (lint validates examples against them; codegen bundles them), issue actions `accept|revert`.
- GET of a document returns `{revision, document}` (+ `status` for targets), because the document schemas are closed.
- `npm run check:api`: Redocly recommended-strict + own checks (every response body has an example, snake_case properties and parameters). Runs in `make lint-web`.
- `web/`: Svelte 5 + Vite + TS 5.9, one page (Targets), typed GET client over generated `api.gen.ts` (`npm run gen`, not committed).
- Makefile: `build-web`, `lint-web`, `test-web` filled (npm ci only when the lock file changes). CI: `actions/setup-node` v7.1.0 (SHA-pinned), Node 26.
- Embed: Go package `web` (`FS()`, `Built()`); `web/dist/.gitkeep` is tracked (force-added; an exception line cannot re-include a file under an ignored `dist/` dir). Nothing imports `web` yet → `plumb` links the UI when T-011 serves it.
- Tests: `make lint test build` → pass (Go tests, vitest 8, build); `bin/plumb version` OK.

# T-017 – OpenAPI contract and web skeleton

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer (pl-architect checks the contract)   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** user approval; merge after T-003   · **Parallel with:** T-003
- **Status:** proposed (do not start before the user approves the architecture)

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
branch · commit · tests · notes

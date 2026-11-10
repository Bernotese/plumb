# T-003 – Scaffold and contracts

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-002 verdict, user approval   · **Parallel with:** –
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
The repo builds, tests and lints. The contracts that all other slice cards code against exist. No behaviour yet.

## Context (read only these)
- `docs/ARCHITECTURE.md` §2, §3, §4, §5, §9
- `docs/DECISIONS.md` D-018 … D-035

## Acceptance criteria
- [ ] Layout as in ARCHITECTURE §9. Go module `github.com/Bernotese/plumb`. `plumb version` and `plumb-agent version` print a version.
- [ ] `internal/wire`: types for enroll, poll, report and the commands `observe`, `apply`, `watch`. Types only.
- [ ] `internal/resource`: `Resource` interface, registry, and JSON Schemas for `file`, `directory`, `package`, `service`, `user`. No implementations.
- [ ] `internal/driver`: `Driver` interface and its types. No implementation.
- [ ] `internal/state/schema`: JSON Schemas for the documents `Image`, `Block`, `Target`.
- [ ] `api/openapi.yaml`: session, images, blocks, targets, plans, runs, issues, events. Every response has an example.
- [ ] `internal/store/migrations/0001_init.sql`: tables for the slice (ARCHITECTURE §3).
- [ ] `web/`: Vite + Svelte + TypeScript skeleton with one page, embedded in `plumb`. `npm run gen` creates the API types from `api/openapi.yaml`.
- [ ] `Makefile`: `build`, `test`, `lint`, `test-guest` (stub), `test-pve` (stub). A GitHub Actions workflow runs lint, test and build on every PR.
- [ ] Tests: `make lint test build` passes on a clean checkout. CI is green.

## Out of scope
- Any behaviour behind the interfaces.
- Secrets, tokens, export, LXC (later cards).

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-003 – Go scaffold and contracts

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-002 verdict, user approval   · **Parallel with:** T-017
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
The Go side builds, tests and lints. The Go contracts that the other slice cards code against exist. No behaviour yet.

## Context (read only these)
- `docs/ARCHITECTURE.md` §2, §3, §5, §9
- `docs/DECISIONS.md` D-018 … D-039

## Acceptance criteria
- [ ] Layout as in ARCHITECTURE §9. Go module `github.com/Bernotese/plumb`. `plumb version` and `plumb-agent version` print a version.
- [ ] `internal/wire`: types for enroll, poll, report and the commands `observe`, `apply`, `watch`. A resource has `id`, `type`, `block`, `attrs` and `sensitive` (attribute names). Types only.
- [ ] `internal/resource`: `Resource` interface, registry, and JSON Schemas for `file`, `directory`, `package`, `service` (with `restart_on`), `user`. No implementations.
- [ ] `internal/driver`: `Driver` interface with seven methods (including `Diff`) and its types. No implementation.
- [ ] `internal/state/schema`: JSON Schemas for the documents `Image` (with pinned `sha256`), `Block`, `Target`.
- [ ] `internal/store/migrations/0001_init.sql`: tables for the slice (ARCHITECTURE §3).
- [ ] A test checks the import rules of ARCHITECTURE §2.
- [ ] `Makefile`: `build`, `test`, `lint`, `test-guest` (stub), `test-pve` (stub). A GitHub Actions workflow runs lint, test and build on every PR.
- [ ] Tests: `make lint test build` passes on a clean checkout. CI is green.

## Out of scope
- `api/openapi.yaml` and `web/` (T-017).
- Any behaviour behind the interfaces.
- Secrets, tokens, export, LXC (later cards).

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

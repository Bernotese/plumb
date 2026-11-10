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
- [ ] `internal/wire`: types for enroll, poll, report and the commands `observe`, `apply`, `watch`. A resource has `id`, `type`, `block`, `attrs` and `sensitive` (attribute names). A change of a sensitive attribute has only a `changed` flag, no `from` and no `to`. Types only.
- [ ] `internal/resource`: `Resource` interface, registry, and JSON Schemas for `file`, `directory`, `package`, `service` (with `restart_on`), `user`. No implementations.
- [ ] `internal/driver`: `Driver` interface with seven methods (including `Diff`) and its types. No implementation.
- [ ] `internal/state/schema`: JSON Schemas for the documents `Image` (with pinned `sha256`), `Block`, `Target`.
- [ ] `internal/store/migrations/0001_init.sql`: tables for the slice (ARCHITECTURE §3).
- [ ] A test checks the import rules of ARCHITECTURE §2.
- [ ] `Makefile`: `build`, `test`, `lint`, `test-guest` (stub), `test-pve` (stub), `build-web` and `lint-web` (stubs that T-017 fills). A GitHub Actions workflow runs lint, test and build on every PR.
- [ ] Tests: `make lint test build` passes on a clean checkout. CI is green.

## Out of scope
- `api/openapi.yaml` and `web/` (T-017).
- Any behaviour behind the interfaces.
- Secrets, tokens, export, LXC (later cards).

## Plan (architect, optional)

## Result (owner, max 8 lines)
T-003 done · branch `pl-architect/T-003-scaffold-contracts` · tests: `make lint test build` → pass (local and CI)
- Contracts: `internal/wire`, `internal/resource` (+5 schemas), `internal/driver`, `internal/state` (+3 schemas), `internal/store/migrations/0001_init.sql`.
- `internal/rules_test.go` enforces the import rules of ARCHITECTURE §2, the binary rules and the dependency allowlist (D-035).
- Three small contract changes, also in ARCHITECTURE: `api` may import `store`; new table `binding` (document → driver id); `Driver.Diff` returns an error for create-only changes.
- Names may contain `.` (needed for `ubuntu-26.04`). A target may use the same Block more than once.
- Stubs `test-guest`, `test-pve` fail with a message until T-007 / T-012 fill them. `build-web`, `lint-web`, `test-web` pass as stubs for T-017. `go.mod` ignores `web/node_modules`.
- Tools: Go 1.27.3 and GNU make are in `~/.local/bin`. staticcheck runs with `go run` (pinned in the Makefile).

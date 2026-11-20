# T-109 – Flaky `TestAgentUpdateCommand`: "guard left after confirm"

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –   · **Parent:** T-089 (slice 3 fix)

## Goal
Find out why `internal/agent TestAgentUpdateCommand` failed once under `-race` in CI (run 43402777645, docs-only PR #206): `update_agent_test.go:66: guard left after confirm`. Fix the test or the code.

## Context (read only these)
- `internal/agent/update_agent_test.go` and the update/seal code it calls (T-091: seal removes `.prev` and the guard)

## Acceptance criteria
- [x] The cause is named: a test that reads too early, or a real path where the guard stays after the confirm. A real path is a product bug: report it to pl-lead before a larger fix
- [x] Fix with a small diff (≤ ~150 lines); no sleep-based waiting if a signal exists
- [x] 200 runs of the test green (CI has `-race`; say what you ran locally)
- [x] `make lint test build` green; PR

## Out of scope
- `cmd/plumb TestServeNoAnswer` (planned in slice 4)

## Result (owner, max 8 lines)
branch · commit · tests · notes

- Cause: the test observes marker removal before `confirm` finishes removing the guard.
- Fix: wait for an observe result, sent after synchronous confirmation returns; then assert marker and guard removal.
- Local: `go test -run '^TestAgentUpdateCommand$' -count=200 ./internal/agent` passed (25.997s).
- Local `CGO_ENABLED=1 go test -race ...` could not compile: no `gcc`/C compiler installed. CI runs race checks.
- `make lint test build` passed. Product code is unchanged.

# T-109 – Flaky `TestAgentUpdateCommand`: "guard left after confirm"

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –   · **Parent:** T-089 (slice 3 fix)

## Goal
Find out why `internal/agent TestAgentUpdateCommand` failed once under `-race` in CI (run 43402777645, docs-only PR #206): `update_agent_test.go:66: guard left after confirm`. Fix the test or the code.

## Context (read only these)
- `internal/agent/update_agent_test.go` and the update/seal code it calls (T-091: seal removes `.prev` and the guard)

## Acceptance criteria
- [ ] The cause is named: a test that reads too early, or a real path where the guard stays after the confirm. A real path is a product bug: report it to pl-lead before a larger fix
- [ ] Fix with a small diff (≤ ~150 lines); no sleep-based waiting if a signal exists
- [ ] 200 runs of the test green (CI has `-race`; say what you ran locally)
- [ ] `make lint test build` green; PR

## Out of scope
- `cmd/plumb TestServeNoAnswer` (planned in slice 4)

## Result (owner, max 8 lines)
branch · commit · tests · notes

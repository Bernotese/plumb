# T-021 – Flow test with the real agent code

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** T-010 (merged), T-018 (PR #13 merged)
- **Status:** draft ready; requires PR #8 and PR #13 first

## Goal
The open T-010 criterion is closed: the core flow is proven with the real agent code, not only with the fake agent.

## Context (read only these)
- `internal/plan` flow tests and `internal/link/linktest` (fake agent) – the pattern to follow.
- `internal/agent` (T-018).
- pl-reviewer's re-review comment on PR #14 (no-target Plan variant).

## Acceptance criteria
- [x] One in-process test uses `driver/fake`, the real `internal/link` and the real `internal/agent` code: create target → approve → apply → change a file → Issue opens → `revert` closes it.
- [x] D-075 case: a second file drifts between plan and apply → the agent refuses, the Run fails, a drift Issue opens, the file is not overwritten.
- [x] Concurrent approval of a Plan **without targets**: exactly one winner through the DB transaction guard (the busy guard does not apply).
- [x] Tests: `make lint test` passes, also with `-race` in CI. Test-only diff, aim ≤ ~150 lines.

## Out of scope
- Production code changes. Report a bug instead.

## Result (owner, max 8 lines)
`pl-codex-qa/T-021-real-agent-flow` · see draft PR commits.
Based on remote T-018 `29aa0a4`, with origin/main `8f591ab` merged (`ea0e811`).
New test-only code: `internal/issue/real_agent_flow_test.go` (137 lines).
Focused tests and `make lint test` pass locally; no C compiler for local race checks.
CI `make test-race` passed on `f07a8a2` (run `42750604704`).
Draft PR #21 must wait for PR #8 and PR #13.

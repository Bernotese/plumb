# T-021 – Flow test with the real agent code

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** T-010 (merged), T-018 (PR #13 merged)
- **Status:** waits for T-018

## Goal
The open T-010 criterion is closed: the core flow is proven with the real agent code, not only with the fake agent.

## Context (read only these)
- `internal/plan` flow tests and `internal/link/linktest` (fake agent) – the pattern to follow.
- `internal/agent` (T-018).
- pl-reviewer's re-review comment on PR #14 (no-target Plan variant).

## Acceptance criteria
- [ ] One in-process test uses `driver/fake`, the real `internal/link` and the real `internal/agent` code: create target → approve → apply → change a file → Issue opens → `revert` closes it.
- [ ] D-075 case: a second file drifts between plan and apply → the agent refuses, the Run fails, a drift Issue opens, the file is not overwritten.
- [ ] Concurrent approval of a Plan **without targets**: exactly one winner through the DB transaction guard (the busy guard does not apply).
- [ ] Tests: `make lint test` passes, also with `-race` in CI. Test-only diff, aim ≤ ~150 lines.

## Out of scope
- Production code changes. Report a bug instead.

## Result (owner, max 8 lines)
branch · commit · tests · notes

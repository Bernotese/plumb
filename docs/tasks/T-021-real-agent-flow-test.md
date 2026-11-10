# T-021 – Flow test with the real agent code

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** T-010 (merged), T-018 (PR #13 merged)
- **Status:** approved slice 2 coverage restored; local checks pass; awaiting CI

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
- [x] Slice 2: encrypted secret fixture, clear apply delivery, D-139 masking, D-156 observe omission, unverified/ignored changes, D-160 dependent restart, and drained command capture.
- [x] Tests: `make lint test` passes, also with `-race` in CI. Test-only diff, aim ≤ ~150 lines.

## Out of scope
- Production code changes. Report a bug instead.

## Result (owner, max 8 lines)
`pl-codex-qa/T-021-real-agent-flow` · PR #21.
Merged origin/main `d02117b` without rebase.
Restored the complete approved test and fixture helpers byte-for-byte from `2bafdd9`.
Retained `docs/tasks/T-041-qa.log.md` with the slice 2 coverage and review context.
Diff against main: this card, the QA log, real-agent flow tests, and fixture helpers only.
`make lint test` and focused flow/concurrent approval tests ×10 pass; CI with guest tests pending.

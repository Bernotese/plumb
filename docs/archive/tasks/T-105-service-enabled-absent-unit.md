# T-105 – Service "stopped + enabled" with an absent unit: Plan hides the enable

- **Owner:** pl-codex-fix (if not trivial: pl-reviewer)   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** T-103 (merged)   · **Parent:** T-089 (follow-up of T-103)

## Goal
A `service` with state `stopped`, `enabled: true` and an absent unit gives no Plan entry (`Diff` in `internal/resource/service.go` returns nil for an absent unit with desired stopped). The first apply is then refused. No starter Block uses this, but a user Block can.

## Context (read only these)
- PR #204 review comment of pl-architect (the fix idea)
- `internal/resource/service.go` (`Diff`), `resource.NewUnitCovers`

## Acceptance criteria
- [x] In that case the Plan shows only `enabled null→true`
- [x] `NewUnitCovers` accepts `enabled false→true` when the state is not planned, under the same maker rule as T-103 (no wider exception)
- [x] One test for the Plan entry, one for the cover
- [x] `make lint test build` passes; diff ≤ ~80 lines

## Out of scope
- Any other change to the cover rule

## Plan (architect, optional)

## Result (owner, max 8 lines)
pl-codex-fix/T-105-service-enabled · 6ce242a · `make lint test build` passed · Plan and narrow maker-cover regression tests; 28 code/test lines changed.

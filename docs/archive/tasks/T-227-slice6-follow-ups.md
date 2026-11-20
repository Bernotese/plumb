# T-227 – Slice 6 follow-ups (ROADMAP rows 28 … 32)

- **Owner:** pl-reviewer (implementer; a Codex card, Codex is at its weekly limit)   · **Reviewer:** pl-backend   · **QA:** –
- **Size:** S (one PR per ROADMAP row, each ≤ ~150 lines)   · **Depends on:** – (row 32's field after T-217)

## Goal
Slice 7, child card **C11** of T-215: the small open points of slice 6.

## Context (read only these)
- Plan: `docs/tasks/T-215-slice-7-plan.md` section "Plan" (until PR #321 is merged: `git show origin/pl-architect/T-215-slice-7-plan:docs/tasks/T-215-slice-7-plan.md`), decisions `docs/decisions/T-215.md` on that branch (D-398 … D-415), ROADMAP `docs/plans/ROADMAP.md` on that branch. Row C11 and ROADMAP rows 28 … 33 (33 is dropped, with its reason).

## Acceptance criteria
- [ ] One small PR per row 28 … 32 with what that row says; migration 0026 for `unbind_pending` STRICT (data kept; test on a DB with rows).
- [ ] `make lint test` per PR; name the CI job that proves each (D-413); report each PR to `pl-lead`.

## Out of scope
- Anything of slice 7 itself.

## Result (owner, max 8 lines)
branch · commit · tests · notes

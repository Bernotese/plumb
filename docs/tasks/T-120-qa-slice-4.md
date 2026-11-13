# T-120 – QA for slice 4

- **Owner:** pl-codex-qa   · **Reviewer:** –   · **QA:** –
- **Size:** M   · **Depends on:** each card   · **Parent:** T-107 (C10)

## Goal
Tests against T-112 … T-119 and three mutation audits.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C10, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-120.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] Per merged card: one narrow test task (pl-lead sends each as its own prompt)
- [ ] Mutation audit of the backup crypto, of the restored mode (every writer), of the safe-apply rollback
- [ ] Findings to pl-lead; log in `docs/tasks/T-120.log.md`

## Out of scope
- Fixes larger than ~150 lines (back to the author)
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-117 – Agent command `grow`

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-111   · **Parent:** T-107 (C7)

## Goal
The agent grows the root file system of a VM after a disk grow.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C7, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-117.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] `growpart` + `resize2fs` / `xfs_growfs` on the root file system only, and only when it is the last partition; else a clear failure text
- [ ] No shell, fixed argv; idempotent (no change when nothing to grow)
- [ ] One guest test; small diff (≤ ~150 lines); `make lint test build` green

## Out of scope
- Other file systems or partitions
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

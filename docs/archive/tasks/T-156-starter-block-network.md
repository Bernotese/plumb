# T-156 – Starter Block network

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-157)
- **Size:** S   · **Depends on:** T-150 merged

## Goal
Slice 5, child card **C11** of T-138. A starter Block `network` with a render test.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan", decisions D-327 … D-347 (`docs/DECISIONS.md`; until merged in `docs/decisions/T-138.md`, `T-144.md`), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §17. Read row C11 of the child card table, the contracts, the risks, and the review comments of PR #241 / #243 that name this row.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C11 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Example values only from the documentation ranges (192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24).
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-156.md`.
- [ ] No new token right. Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

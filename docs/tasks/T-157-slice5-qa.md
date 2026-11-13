# T-157 – Slice 5 QA: role matrix, races, fuzz and mutation audits

- **Owner:** pl-codex-qa   · **Reviewer:** –   · **QA:** pl-codex-qa (T-157)
- **Size:** M   · **Depends on:** per card, after that card is merged

## Goal
Slice 5, child card **C12** of T-138. Second opinion on slice 5 by tests.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan", decisions D-327 … D-347 (`docs/DECISIONS.md`; until merged in `docs/decisions/T-138.md`, `T-144.md`), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §17. Read row C12 of the child card table, the contracts, the risks, and the review comments of PR #241 / #243 that name this row.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C12 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Role × operation matrix read from `api/openapi.yaml` that **calls** every operation (review M2). Parallel demote of the two last admins. Fuzz + mutation audit of `internal/trust`. Mutation audit of the copy rule (T-148) and of the network rollback (T-150). Commit before each mutant; count build failures. Small PRs, one per part; log in `docs/tasks/T-157.log.md`.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-157.md`.
- [ ] No new token right. Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

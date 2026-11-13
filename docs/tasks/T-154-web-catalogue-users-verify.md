# T-154 – Web: catalogue, users page, role-aware buttons, Image verify

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-157)
- **Size:** M   · **Depends on:** T-144 (mock), T-146, T-147, T-152; after T-153 (same screens)

## Goal
Slice 5, child card **C9b** of T-138. The UI for the new server features of slice 5.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan", decisions D-327 … D-347 (`docs/DECISIONS.md`; until merged in `docs/decisions/T-138.md`, `T-144.md`), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §17. Read row C9b of the child card table, the contracts, the risks, and the review comments of PR #241 / #243 that name this row.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C9b of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] e2e tests and a `browser-shot` picture per new screen in the PR (`serve --dev --dev-demo`, no host data). A one-time password is shown once and never stored in the page state after leaving it.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-154.md`.
- [ ] No new token right. Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

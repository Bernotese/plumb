# T-155 – npm audit refresh (deepmerge)

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S (Codex)   · **Depends on:** –

## Goal
Slice 5, child card **C10** of T-138. Close the 2 low `npm audit` findings (D-341).

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan" (until PR #241 is merged: `git show origin/pl-architect/T-138-road-to-release:docs/tasks/T-138-road-to-release-slice-5-plan.md`), decisions `docs/decisions/T-138.md` on the same branch. Read row C10 of the child card table, the contracts, the risks.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C10 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] `npm audit` in `web/` shows 0 findings, or the report says exactly why one stays. Only `web/package.json` and `web/package-lock.json` change. `make lint test build` passes.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-155.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any new token right. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

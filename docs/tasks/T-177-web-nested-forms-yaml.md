# T-177 – Web: nested parameter forms and YAML mode

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** –

## Goal
Slice 6 part A, child card **A4b** of T-172. Object and array-of-object parameters render as forms; Target and Image have a YAML mode (D-363).

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions `docs/decisions/T-172.md` on the same branch (D-360 … D-375). Read row A4b of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A4b of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] The YAML mode shows secret references only, never a value; a YAML edit goes through the same server validation and Plan as the form. e2e + mock pictures.
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-177.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures in a PR only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

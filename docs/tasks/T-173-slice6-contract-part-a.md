# T-173 – Slice 6 contract part A

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** –

## Goal
Slice 6 part A, child card **A1** of T-172. Write the contracts of part A so that A2, A3, A5, A6a, A6b, A8 can start.

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions `docs/decisions/T-172.md` on the same branch (D-360 … D-375). Read row A1 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A1 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] The three checks the plan lists before writing (old agents on 429; token reads its own permissions; MCP Go SDK license and age) are answered with a source in the PR. A host read goes through `pl-lead` (one agent on the host).
- [ ] Take in the findings of the plan review of PR #272 that touch the contract.
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-173.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures in a PR only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

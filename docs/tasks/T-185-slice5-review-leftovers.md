# T-185 – Review leftovers of slice 5

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S (Codex)   · **Depends on:** T-183 first (same agent)

## Goal
Slice 6 part A, child card **A11** of T-172. The small open notes of the reviews #241 … #271 (D-375).

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions `docs/decisions/T-172.md` on the same branch (D-360 … D-375). Read row A11 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A11 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] The netplan probe leaves CI only after you checked that `TestGuestNetwork` covers what it measured; say so in the PR.
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-185.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures in a PR only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

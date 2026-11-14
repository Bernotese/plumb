# T-196 – Target form writes only values that differ from the Block default

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-186)
- **Size:** S   · **Depends on:** T-177 merged (same files)

## Goal
Slice 6 part A, child card **A15** of T-172. A change of a Block default must reach Targets that never chose a value (D-376).

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions D-360 … D-376 (`docs/decisions/T-172.md` on that branch), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §18 (on main). Read row A15 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A15 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] New saves write no default value and no empty `overrides`. Existing documents are not rewritten: the form shows a stored value equal to the default as "fixed to the current default" and offers "Follow the Block default" per parameter, as a normal proposal.
- [ ] Tests: save, reload, a Block default change reaches the Target; a stored pin stays until the person unpins it.
- [ ] A card that adds an API operation updates `web/src/lib/roleHints.ts`, the tier test and the contract marks in the same PR. A new resource type or attribute gets the opaque/guest-value classification test (D-359).
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-196.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

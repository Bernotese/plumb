# T-176 – Web: settings tab, Accept into a parameter, rate-limit message, live_watch note

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-186)
- **Size:** M   · **Depends on:** T-173 (mock), T-174, T-175; after T-196

## Goal
Slice 6 part A, child card **A4a** of T-172. The UI for the server features of part A.

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions D-360 … D-376 (`docs/decisions/T-172.md` on that branch), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §18 (on main). Read row A4a of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A4a of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Also from the reviews: with more than 20 documents the Approve hint stays (#269 n2); a token-made Plan reason is shown below the changes and labelled (#272 F4).
- [ ] A card that adds an API operation updates `web/src/lib/roleHints.ts`, the tier test and the contract marks in the same PR. A new resource type or attribute gets the opaque/guest-value classification test (D-359).
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-176.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

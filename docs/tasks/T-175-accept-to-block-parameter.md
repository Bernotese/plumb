# T-175 – Accept to a Block parameter

- **Owner:** pl-architect (implementer)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-186)
- **Size:** L   · **Depends on:** T-173 merged

## Goal
Slice 6 part A, child card **A3** of T-172. Accept adopts a drift value into the Block parameter that made it, when that is provably exact; else raw mode as today (BRIEF §5.8, D-362).

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions D-360 … D-376 (`docs/decisions/T-172.md` on that branch), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §18 (on main). Read row A3 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A3 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Every Accept stays human-only (D-323) and inside the copy rule (D-331, D-359); Accept on `firewall:*` stays refused (T-125). Tests for both.
- [ ] The re-render proof: after the parameter change nothing else of any Target changes; a parameter that feeds another resource or another Target → raw. Fuzz or property test for the template inverse.
- [ ] A card that adds an API operation updates `web/src/lib/roleHints.ts`, the tier test and the contract marks in the same PR. A new resource type or attribute gets the opaque/guest-value classification test (D-359).
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-175.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

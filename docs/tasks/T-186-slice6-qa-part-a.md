# T-186 – Slice 6 part A QA

- **Owner:** pl-codex-qa   · **Reviewer:** –   · **QA:** pl-codex-qa (T-186)
- **Size:** M   · **Depends on:** per card, after that card is merged

## Goal
Slice 6 part A, child card **A12** of T-172. Second opinion on part A by tests.

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions D-360 … D-376 (`docs/decisions/T-172.md` on that branch), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §18 (on main). Read row A12 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A12 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] The list of row A12. Plus from the review of #277: a test that ENOBUFS on the netlink socket triggers a check, and a test of the port-0 (kernel-only) filter that runs in CI without a netlink send (both mutants survived).
- [ ] From the review of #281: a case with a non-sensitive report on a writeOnly (secret) parameter → Accept to that parameter is refused (two guards survived mutants).
- [ ] Commit before each mutant; count build failures; small PRs; log `docs/tasks/T-186.log.md`.
- [ ] A card that adds an API operation updates `web/src/lib/roleHints.ts`, the tier test and the contract marks in the same PR. A new resource type or attribute gets the opaque/guest-value classification test (D-359).
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-186.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-188 – README: settings, Accept to a parameter, MCP setup, live drift

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-186)
- **Size:** S   · **Depends on:** T-187 passed

## Goal
Slice 6 part A, child card **A14** of T-172. The user README covers part A.

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions D-360 … D-376 (`docs/decisions/T-172.md` on that branch), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §18 (on main). Read row A14 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A14 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] User text only. MCP setup with a `plan` token and what the client can never do. The 15 min limit text of slice 5 is corrected. One line on the copy rule stays (#270 n2). No release or download text (D-326).
- [ ] A card that adds an API operation updates `web/src/lib/roleHints.ts`, the tier test and the contract marks in the same PR. A new resource type or attribute gets the opaque/guest-value classification test (D-359).
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-188.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

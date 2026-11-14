# T-184 – Prove the system approval of a newer signed pin end to end (F5)

- **Owner:** pl-codex-qa   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** S (Codex)   · **Depends on:** –

## Goal
Slice 6 part A, child card **A10** of T-172. T-158 could not show on the host that a scheduled build takes a newer signed base by `system`. Prove it in CI with a test key and local signed lists (D-369).

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions `docs/decisions/T-172.md` on the same branch (D-360 … D-375). Read row A10 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A10 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] The test key can never be accepted by a release build: a test proves the release binary pins only the vendor keys (build tag `plumbtest` or the plan's way). No private key material of a real vendor; the test private key is marked as test data.
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-184.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures in a PR only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-144 – Slice 5 contract: catalogue, users and roles, copy rule, verify + refresh

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** –

## Goal
Slice 5, child card **C1a** of T-138. Write the contracts so that C2, C3, C4, C8 and C9b can start.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan" (until PR #241 is merged: `git show origin/pl-architect/T-138-road-to-release:docs/tasks/T-138-road-to-release-slice-5-plan.md`), decisions `docs/decisions/T-138.md` on the same branch. Read row C1a of the child card table, the contracts, the risks.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C1a of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] `make check:api` (or the repo's API check) passes; vendor key fingerprints checked from two sources and the sources named in the PR.
- [ ] Take in the findings of the plan review of PR #241 that touch the contract.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-144.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any new token right. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

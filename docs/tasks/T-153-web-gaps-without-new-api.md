# T-153 – Web gaps that need no new API

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** –

## Goal
Slice 5, child card **C9a** of T-138. The UI gets what the API can already do: delete Target/Block/Image by a proposal, plan list and drop, run list, add an Issue comment, compare two revisions, Issue filter by Target (D-338).

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan" (until PR #241 is merged: `git show origin/pl-architect/T-138-road-to-release:docs/tasks/T-138-road-to-release-slice-5-plan.md`), decisions `docs/decisions/T-138.md` on the same branch. Read row C9a of the child card table, the contracts, the risks.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C9a of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Every new screen and action has an e2e test and a `browser-shot` picture in the PR (`serve --dev --dev-demo`, no host data).
- [ ] Delete goes through the normal proposal and plan preview; no direct delete call from the UI.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-153.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any new token right. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

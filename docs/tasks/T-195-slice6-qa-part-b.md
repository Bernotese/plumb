# T-195 – Slice 6 part B QA

- **Owner:** pl-reviewer for T-193, pl-architect for T-190/T-191 (Codex is at its limit; if `codex-usage --route` says codex again: pl-codex-qa)   · **Reviewer:** –   · **QA:** T-195
- **Size:** M   · **Depends on:** per card, after that card is merged

## Goal
Slice 6 part B, child card **B6** of T-172. Second opinion on part B by tests.

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` part B (as updated by T-189), contract `api/openapi.yaml` + `docs/ARCHITECTURE.md` §19, decisions D-372, D-373, D-377, D-378, D-382 … D-389 (until PR #290 is merged: branch `pl-architect/T-189-contract-b`), risks `docs/plans/risks.md` §2, §3 on branch `pl-reviewer/T-140-risk-draft`. Read row B6, the test table of ARCH §19 (each rule names its test) and the review comments of PR #290.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row B6 lists; touch only the files that row gives you (test files next to them need no question; list them in the PR). Another file: ask `pl-lead` first.
- [ ] Every test that ARCH §19 names for this row exists under that name.
- [ ] The list of row B6. Commit before each mutant; count build failures; small PRs; log `docs/tasks/T-195.log.md`.
- [ ] A card that adds an API operation updates `roleHints.ts`, the tier test and the contract marks in the same PR. No new token right. New decisions only with numbers from `pl-lead`.
- [ ] `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. The guest-agent transport (not built, D-377). Slice 7.

## Result (owner, max 8 lines)
branch · commit · tests · notes

## Follow-ups found by QA (pl-lead keeps this list; each gets a card or goes into a PR)
- #300 n1: drop an unused repair token also when the repair succeeded another way (T-190 code) → pl-backend, small follow-up after #298.

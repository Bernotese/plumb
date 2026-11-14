# T-190 – Repair core

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** T-195
- **Size:** L   · **Depends on:** T-189 merged

## Goal
Slice 6 part B, child card **B2** of T-172. A human-only Plan repairs a guest whose agent is dead: bundle, one-time token, new enrollment (D-372).

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` part B (as updated by T-189), contract `api/openapi.yaml` + `docs/ARCHITECTURE.md` §19, decisions D-372, D-373, D-377, D-378, D-382 … D-389 (until PR #290 is merged: branch `pl-architect/T-189-contract-b`), risks `docs/plans/risks.md` §2, §3 on branch `pl-reviewer/T-140-risk-draft`. Read row B2, the test table of ARCH §19 (each rule names its test) and the review comments of PR #290.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row B2 lists; touch only the files that row gives you (test files next to them need no question; list them in the PR). Another file: ask `pl-lead` first.
- [ ] Every test that ARCH §19 names for this row exists under that name.
- [ ] Review R1 of #290: the old agent credential ends only at the re-enrollment (exit 0 / new hello); a failed repair of a merely partitioned agent leaves it as it was (test).
- [ ] Repair Plans: origin `repair`, human-only for every token tier, output limits, no secret in the Run output.
- [ ] A card that adds an API operation updates `roleHints.ts`, the tier test and the contract marks in the same PR. No new token right. New decisions only with numbers from `pl-lead`.
- [ ] `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. The guest-agent transport (not built, D-377). Slice 7.

## Result (owner, max 8 lines)
branch · commit · tests · notes

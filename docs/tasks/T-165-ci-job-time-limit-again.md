# T-165 – CI: the job "lint, test, build" hits its 15 min limit again

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** –

## Goal
PR #259 (run 43541537382, job 132282975113) was cancelled at 15:07 in the e2e step: lint 1.6 min, `make test` 3.6, race detector 7.6, build 0.6, then e2e had < 1 min left. T-136 brought the job to 8–10 min; the tests of slice 5 ate the margin. A cancelled job looks like a failure and costs a rerun.

## Context (read only these)
- `.github/workflows/` (the CI workflow) · `Makefile` (`test`, `test-race`) · `docs/tasks/T-136-ci-install-tests-too-slow.md`

## Acceptance criteria
- [ ] Measure first, from the logs of the last 3 green main runs: time per step and the 5 slowest packages under `-race` (table in the PR).
- [ ] Bring the job back to ≤ ~10 min with a margin, without dropping a check. Preferred: the race detector as its own job that runs beside the others (no extra wait), or the slowest race tests made cheaper if one package dominates for a bad reason (say which test and why). Raising the timeout alone is not a fix; a small raise as a guard is fine.
- [ ] Every test that ran before still runs somewhere in CI (list in the PR: where each step runs now).
- [ ] The runner budget: say how many job-minutes one PR run costs before and after.
- [ ] Own worktree and branch, PR to `main`, report to `pl-lead`.

## Out of scope
- Changing what the tests assert. Repo settings.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-043 – Agent refuses an unsafe state directory at start

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (guest test in PR #70 covers the refusal)
- **Size:** S   · **Depends on:** T-018 on `main` (done)

## Goal
The agent does not run with a state directory that other users can read. It stops at start with a clear error.

## Situation
Product note from the PR #70 review: `agent.New()` accepts a `state_dir` with mode 0755 and only logs a WARN. The refusal comes later, at the first write. The state directory holds the credential and `desired.json` with clear secret values (D-159).
Mind the PR #64 review (F1): an error of the temp file cleanup must not stop the start. That rule stays. This card is only about an unsafe directory.

## Context (read only these)
- `internal/agent/store.go` (`New`, `ensure`, `cleanStaleTemps`) and its tests
- `docs/DECISIONS.md` D-055, D-159 · `docs/tasks/T-007.run-review.md` section C

## Acceptance criteria
- [ ] At start: a state directory that is a symlink, is not owned by the agent's user, or has a mode other than 0700 → the agent exits with an error that names the path and the fix. It does not change the mode by itself.
- [ ] A missing directory is created with 0700, as today.
- [ ] A cleanup error still only logs a warning (PR #64, F1).
- [ ] Unit tests for each case. The guest test of PR #70 still passes (adapt it only if it asserted the WARN behaviour; say so).
- [ ] Decision in `docs/decisions/T-043.md` as D-161.
- [ ] Tests: `make lint test` passes; CI incl. the guest job is green.

## Out of scope
- Changes to the credential format or to `desired.json`.

## Result (owner, max 8 lines)
branch · commit · tests · notes

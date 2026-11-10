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
- [x] At start: a state directory that is a symlink, is not owned by the agent's user, or has a mode other than 0700 → the agent exits with an error that names the path and the fix. It does not change the mode by itself.
- [x] A missing directory is created with 0700, as today.
- [x] A cleanup error still only logs a warning (PR #64, F1).
- [x] Unit tests for each case. The guest test of PR #70 still passes (adapt it only if it asserted the WARN behaviour; say so).
- [x] Decision in `docs/decisions/T-043.md` as D-161.
- [x] Tests: `make lint test` passes; CI incl. the guest job is green.

## Out of scope
- Changes to the credential format or to `desired.json`.

## Result (owner, max 8 lines)
- Branch `pl-frontend/T-043-agent-state-dir-fail-fast`. `New` calls `ensure` first and returns its error; cleanup error still only warns.
- `ensure` names the problem (symlink / not a directory / not owned / mode) and the fix. Mode check is now exactly 0700 (0500 was accepted before).
- Tests: `TestNewRefusesUnsafeStateDir` (modes 0755/0750/0701/0500, symlink, file, other owner via `agentUID` seam, missing dir → 0700); `TestNewContinuesWhenTempCleanupFails` now forces the error via `removeFile` seam in a valid 0700 dir. `make lint test` → pass.
- PR #70 guest test (not on main) asserts `New` succeeds with 0755 and must change: expect `New` to fail with "mode 0700" and the mode to stay 0755.

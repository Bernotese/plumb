# T-137 – A restored Plumb silently has no off-host copy

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-114, T-132 (on main)

## Goal
After `--remove --keep-guests` and `--restore` the off-host copy runs again without the person having to remember a flag.

## Context (read only these)
- `install/install.sh`: the kept state of `--remove --keep-guests` (it writes `backup_storage=` empty), `--restore`, the `--restore` line that the installer prints at the end of `--keep-guests`; `install/backup_test.go`

## Acceptance criteria
- [x] `--remove --keep-guests` keeps the name of the backup storage in the kept state. `--restore` without `--backup-storage` uses the kept name and says so; an explicit `--backup-storage <name>` or `--backup-storage none` wins
- [x] The `--restore` line that the installer prints holds the storage when one was set
- [x] When a restore ends with no off-host copy, the installer says it in one clear line at the end (not only in the UI)
- [x] Dry-run shows the copy timer step; fake-PVE tests for the three cases (kept, explicit, none); shellcheck clean; `make lint test build` green; small diff

## Out of scope
- Anything else in the installer; no new token right (D-081)

## Result (owner, max 8 lines)
branch · commit · tests · notes

pl-codex-fix/T-137-restore-keeps-backup-storage · see PR commit · tests: make lint test build → PASS (ShellCheck installed); targeted fake-PVE restore/keep/dry-run tests → PASS · no host access; origin/main merged before PR.

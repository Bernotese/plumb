# T-143 – Small leftovers of slice 4

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (≤ ~100 lines)   · **Depends on:** –

## Goal
Close three small notes that the reviews of slice 4 left open.

## Context (read only these)
- `README.md` section "Backup and restore" (the restore line with `--backup-storage`) · `docs/tasks/T-137-*.md`
- `install/` tests: `wait_booted`, `install/realplumb_test.go`

## Acceptance criteria
- [ ] README: since T-137 the installer keeps the backup storage over `--remove --keep-guests`. Shorten the restore text to what a user must still do. Check the claim against the installer code first; if it is not fully true, say so in the report and change nothing there.
- [ ] A test for `wait_booted` when `systemctl is-system-running` says `degraded` (expected behaviour: read the code and the T-132 card; do not change behaviour).
- [ ] `install/realplumb_test.go` removes its temp dir.
- [ ] Tests: `make lint test` passes. Own worktree and branch, PR to `main`.

## Out of scope
- Any change of installer behaviour.

## Result (owner, max 8 lines)
branch · commit · tests · notes

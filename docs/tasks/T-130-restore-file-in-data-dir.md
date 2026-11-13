# T-130 – `--restore` fails: the installer puts the backup into the data dir (T-122 F5, blocker)

- **Owner:** pl-backend   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S   · **Depends on:** T-113, T-114 (on main)

## Goal
`install.sh --restore` works end to end. Today it pushes the backup to `/var/lib/plumb/restore.pbk`, and `plumb restore` refuses a data dir that is not empty (D-318), so every restore fails.

## Context (read only these)
- `docs/tasks/T-122.log.md` F5 (branch `pl-backend/T-122-slice4-real-host`); `install/install.sh` (`RESTORE_IN_LXC`, around line 1710); `cmd/plumb/restore.go` (`cleanRestoreRest`); `install/backup_test.go:547`

## Acceptance criteria
- [ ] The installer pushes the backup to a place outside the data dir (e.g. `/var/lib/plumb-restore/restore.pbk`, dir 0700 owner `plumb`), and removes file and dir after the restore, also after a failed one. `plumb restore` keeps its strict rule (no new allowed name in the data dir)
- [ ] A test runs the real `plumb restore` binary on the layout that the installer makes (not only command strings), so the two sides are tested together. Look for the same kind of gap in the other installer → CLI calls (`backup now`, `backup verify`, `key restore`) and close it the same way
- [ ] A rerun of `--restore` on a host with the half-made LXC of the failed run works (restoring mark, D-314)
- [ ] `make lint test build` green; small diff; say in the PR what the host needs for the rerun

## Out of scope
- The host run itself (T-122, after pl-lead's go)
- No new token right (D-081)

## Result (owner, max 8 lines)
branch · commit · tests · notes

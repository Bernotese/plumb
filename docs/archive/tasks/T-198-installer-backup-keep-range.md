# T-198 – Installer and controller agree on the range of the backup keep value

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex, ≤ ~40 lines)   · **Depends on:** T-174 merged

## Goal
Found in T-174: `install.sh` accepts `--backup-keep 1…999`, the controller refuses a value above 100 (D-361). An install with a larger value would write a setting that `serve` refuses.

## Acceptance criteria
- [ ] First say what happens today with `--backup-keep 500` (service fails to start? value ignored?) with file:line.
- [ ] One range in both places (the controller's), a clear installer error before any change on the host, a test in `install/`. Check the copy-timer keep the same way.
- [ ] `make lint test` passes; PR to `main`; report.

## Result (owner, max 8 lines)
branch `pl-reviewer/T-198-backup-keep-range` (Codex limit: done by pl-reviewer) · tests: `make lint test` → pass
- Before: `install.sh:310` took `--backup-keep 1 … 999` and wrote `PLUMB_BACKUP_KEEP` into plumb.env (`install.sh:1522`); `plumb serve` refuses > 100 (`cmd/plumb/serve.go:97`, `backup.MaxKeep`), so `plumb.service` failed and the installer died with "plumb did not start in the LXC" (`install.sh:1481`) after it had changed the host; a repair run would stop a running controller.
- Now: `--backup-keep` 1 … 100 (the controller's range), refused before any change with "Nothing was changed."; `--backup-copy-keep` stays 1 … 999 (only the copy script on the host reads it; it needs a positive number, no controller limit).
- `install/backupkeep_test.go` takes the limit from `backup.MaxKeep` (101, 500, 007, copy 1000 refused before any call; 100 and copy 999 accepted and written).

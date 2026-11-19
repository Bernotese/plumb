# T-265 – The installer's printed restore line names every flag of the install (host finding, T-263 step 6)

- **Owner:** pl-reviewer (pl-backend is on the host)   · **Reviewer:** pl-backend (installer = critical)   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
Found on the real host (T-263 6(3)): `install.sh --remove --keep-guests` prints a line "to restore, run …". That line lacks `--guest-backup-storage`, `--guest-backup-clear`, `--extra-bridge` and `--extra-vlan`. A user who copies it gets an install without those rights lines: guest backups and the extra networks stop working after the restore, and nothing tells why.

## Acceptance criteria
- [ ] The printed line holds every flag that shapes rights or storage of the install as it is now (read from `install.state` / the same source the repair run uses), in a form that can be pasted; nothing secret in it (no token, no key, no password).
- [ ] The same for every other place that prints a "run this again" line (repair hint, upgrade hint, the README's restore section): one function builds it; list the places in the PR.
- [ ] If a value cannot be known (an old `install.state` without the field), the line says so plainly instead of leaving the flag out.
- [ ] Tests with the fake PVE: an install with a guest backup storage, one extra bridge and two VLANs → the remove's line holds them all; a clean install from that line gives the same ACL rows (the `readme_rights_test` helper can compare). Mutant: drop one flag.
- [ ] No new flag, no ACL change. `make lint test`; evidence in the PR first; PR to `main`; report.

## Result (owner, max 8 lines)
- `install.state` holds `bridge`, `disk_storage`, `image_storage`, `extra_nets`, `guest_backup_storage`, `guest_backup_clear`; `--remove --keep-guests` keeps them. One function, `rerun_flags`, builds the flags (plus `--pool`, `--snippet-dir`, `--image-dir` when not default) from it.
- Places that use it: the restore line at the end of `--remove --keep-guests`, its "Then:" plan line, the repair hint "No off-host backup copy" after a restore, and the die "LXC … has a restored database; run … (a repair run)". New: a `--restore` whose rights flags differ from the kept install warns before the confirm and prints the old flags. The README restore section says to paste the whole line. `acl_failed` ("the same command again") and the image rollback hint name no flags; the controller's texts already say "with every --extra-bridge and --extra-vlan".
- Old state: the line ends with `<UNKNOWN: … add them by hand: --flags>`; bash refuses it (syntax error). A value that the flag checks would refuse is never printed (same `<UNKNOWN>`).
- Tests (`install/restore_line_test.go`): guest backup storage + `--extra-bridge vmbr1` + `--extra-vlan vmbr9:20,30` → exact line; a restore from that line gives the same ACL rows; warning tests; old state; bad value; non-defaults. 13 of 13 mutants caught. `make lint test` rc 0.
- No new flag, no ACL change.

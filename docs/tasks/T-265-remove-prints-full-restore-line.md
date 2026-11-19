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
branch · commit · tests · notes

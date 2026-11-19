# T-269 – A restore keeps the off-host copy working, and the printed line names every flag of the install (host findings F2 and L1 of T-263)

- **Owner:** pl-reviewer (pl-backend is on the host) · **Reviewer:** pl-backend (installer = critical) · **QA:** –
- **Size:** S · **Depends on:** T-265 (merged)

## Goal

Two findings of the host re-run with RC3 (T-263, steps R3-3 and R3-4).

**F2 (a bug):** `install.sh --restore` WITHOUT `--backup-storage` (the README's restore line) runs `check_backup_storage` in a subshell inside `check_copy_setting`, so `COPY_TYPE` and `COPY_PATH` are lost. The copy timer's config gets an empty type and path, and every daily off-host copy of Plumb's backup fails (it fails closed and writes nothing). Nothing tells the user. The R1 run of T-263 had it.

**L1 (a gap of T-265):** the printed restore/repair line holds the six rights keys only. Not covered: `--lxc-id`, `--vmid-range`, `--nameserver`, `--searchdomain`, `--template-storage`, `--backup-time`, `--backup-keep`, `--backup-copy-*`, `--backup-max-size`, `--url`, `--ca`. A user who set one gets the default after following the line (another VMID range, other DNS for new VMs, another backup schedule, another URL).

## Acceptance criteria

- [ ] F2: the copy setting is set in the main shell on every path (install, upgrade, repair, restore, with and without `--backup-storage`); a restore without the flag keeps the copy of the kept install or stops with a clear text – never an empty config. The installer checks the written timer config (type and path not empty) before it ends. Tests for each path with the fake PVE; the test fails on today's code.
- [ ] An install that already has the empty config (made by an older restore): the next run (upgrade or repair) repairs it or says so; and Plumb itself shows a failed off-host copy to the admin (say what the controller shows today when the copy unit fails; if nothing: say so in the report, `pl-lead` decides a card).
- [ ] L1: `install.state` holds every flag that the user gave and that shapes the install (list them all in the PR as a table: flag, kept in state, in the line); the line names each one that is not the default; defaults stay out on purpose (say so in `--help`). `--lxc-id`: the line names it when it was given. Values through `plain_value` as in T-265; an old state gives `<UNKNOWN …>` for what it cannot know.
- [ ] README restore section: the line to use is the one the remove prints; the hand-written example names `--backup-storage`.
- [ ] No new flag, no ACL change. Mutants. `make lint test`; evidence in the PR first; PR to `main`; report.

## Result (owner, max 8 lines)

- F2: `check_copy_setting` runs `check_backup_storage` in the main shell on the restore path too. `install_copy_timer` refuses a config without type or path, and reads it back after the write. A repair run rewrites a broken config, or names it when the storage fails its checks.
- What Plumb shows today with a broken config: the copy script fails ("storage … has the type ; only dir, …") and writes a `failed` status, so Settings → Backups shows **Copy failed** after the first daily run. Before that run (up to one day after a restore) it shows **No copy** (the new LXC has no status file yet).
- L1: `RERUN_TABLE` (16 keys) in `install.state`; the restore and repair lines name each one that is not the default; `--help` and the README say defaults are left out on purpose. The table of flags is in the PR.
- Tests: `install/restore_t269_test.go` (6 tests; the F2 ones fail on the old installer). 12 mutants: 11 caught; the read-back after the write (M3) survives, as it only catches a failed write on disk. `make lint test` rc 0.

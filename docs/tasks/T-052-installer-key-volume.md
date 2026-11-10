# T-052 – Installer: key volume and `plumb key init`

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** real-host run (T-013 items)
- **Size:** S–M   · **Depends on:** PR #26 (T-013, same branch)   · **State:** go (the user approved the Proxmox track; the test host is ready)

## Goal
The minimal installer gives a controller that starts: it creates the key volume and runs `plumb key init` on a real terminal. Without this, `plumb serve` refuses to start (no key file, D-086) and the real install of T-013 gives a crash loop.

## Context (read only these)
- `docs/tasks/T-013-minimal-installer.md`, section "Follow-up" (the four places in `install/install.sh`)
- `docs/DECISIONS.md` D-086, D-102, D-081 … D-084
- `docs/tasks/T-013.log.md` (dry runs on the test host)

## Acceptance criteria
- [ ] `pct create` adds a key volume at `/var/lib/plumb/key` (own volume, `backup=0`), owner `plumb`, mode 0700.
- [ ] When `master.key` is missing, the installer runs `plumb key init --data /var/lib/plumb` as user `plumb` on a real TTY (`pct enter` or `lxc-attach`, not `pct exec`), before the first start of `plumb.service`. A repair run with a key skips it.
- [ ] Without a terminal the installer stops before any change and says why. `--dry-run` only prints the step.
- [ ] The installer never stores, logs or repeats the recovery key. Only `plumb key init` shows it.
- [ ] `--remove` says in its confirmation that the database is unreadable without the recovery key, and destroys the key volume.
- [ ] A static address for the LXC is possible (`--lxc-ip`, `--lxc-gw` or what exists) and the dry run prints a warning when the LXC uses DHCP (the snippet holds the LXC address).
- [ ] Tests in `./install/` for each point (fake PVE). `shellcheck` runs in CI.
- [ ] New decisions: D-177 … D-179 in `docs/decisions/T-052.md`.

## Out of scope
- The real install on the test host (T-013 step 3, after the review and the go of pl-lead).
- Tarball download, upgrades, backups.

## Result (owner, max 8 lines)
- `pl-backend/T-013-minimal-installer` (PR #26) · tests: `go test -count=1 ./install/` ok, `shellcheck -x` clean (v0.10.0).
- Key volume `mp0 <disk>:1,mp=/var/lib/plumb/key,backup=0`; mount point checked; `chown plumb`, `chmod 0700` (D-177).
- `plumb key init` via `lxc-attach … runuser -u plumb`, between `systemctl enable` and the first start; never captured; needs a terminal before any change; repair run with a key skips it (D-178).
- An old Plumb LXC without the key volume (or without `backup=0`) is refused before any change.
- `--remove` names the key volume, warns about the recovery key, frees a volume that `pct destroy` left.
- DHCP warning in dry and real runs; `--lxc-ip`/`--lxc-gw` already existed (D-179). New tests: `install/key_test.go`.
- Note for T-013 step 3: the real install shows the recovery key, so the user runs it on a terminal (`ssh -t`), not an agent.

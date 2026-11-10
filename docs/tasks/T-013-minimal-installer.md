# T-013 – Minimal installer for the test host

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-012; needs the test host   · **Parallel with:** T-010
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
One script prepares a Proxmox host for Plumb, so the real-host tests and the first real run work.

## Context (read only these)
- `docs/ARCHITECTURE.md` §5, §7
- `docs/BRIEF.md` §5.1
- `docs/DECISIONS.md` D-006, D-007, D-032

## Acceptance criteria
- [ ] `install/install.sh` creates: role `Plumb`, user `plumb@pve`, token with privilege separation, pool, snippet directory storage, unprivileged Debian LXC with that directory mounted, systemd service.
- [ ] The role has exactly the privileges of ARCHITECTURE §7. The script prints them before it changes anything.
- [ ] The token is stored only inside the LXC (mode 0600). The script never prints it.
- [ ] The script is idempotent. `install.sh --remove` removes everything it created, after a confirmation.
- [ ] It ends with the UI address and the initial login.
- [ ] Negative checks on the test host: the token cannot change a guest outside the pool, cannot set `hookscript`, and cannot change permissions.
- [ ] Tests: `shellcheck install/install.sh` passes. The checks above run in `make test-pve`.

## Out of scope
- Tarball download and checksum, upgrades, backups (full installer comes later).

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

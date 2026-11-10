# T-013 – Minimal installer for the test host

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** user approval; needs the test host   · **Parallel with:** host track: before T-012
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
One script prepares a Proxmox host for Plumb, so the real-host tests and the first real run work.

## Context (read only these)
- `docs/ARCHITECTURE.md` §5, §7
- `docs/BRIEF.md` §5.1
- `docs/DECISIONS.md` D-006, D-007, D-025, D-032

## Acceptance criteria
- [ ] `install/install.sh` creates: role `Plumb`, user `plumb@pve`, token with privilege separation, pool, storage `plumb-snippets` (content `snippets` only, root-owned), the static vendor snippet, an unprivileged Debian LXC without any host mount, a systemd service.
- [ ] The snippet holds the Plumb CA, the agent URL and an idempotent install command. It holds no secret. The install command works on stock Debian 13 and Ubuntu 26.04 cloud images without extra packages.
- [ ] `install.sh --snippet-only --ca <file> --url <url>` writes only the snippet (for a controller that runs on the dev VM).
- [ ] The role has exactly the privileges of ARCHITECTURE §7. The script prints them before it changes anything. It copies `pve-root-ca.pem` into the LXC.
- [ ] The token is stored only inside the LXC (mode 0600). The script never prints it.
- [ ] The script is idempotent. `install.sh --remove` removes everything it created, after a confirmation. It ends with the UI address and the initial login.
- [ ] Negative checks with the token (in `make test-pve`): it cannot upload or change a file in `plumb-snippets`; cannot set `cicustom` to a volume of another storage; cannot set `hookscript`; cannot change a guest outside the pool; cannot create a VM in another pool; cannot attach a disk of a foreign VM; cannot `import-from` a foreign volume; cannot change permissions.
- [ ] Facts to record in the Result: is `Pool.Audit` needed (never `Pool.Allocate`); which rights `cicustom` and `smbios1` need.
- [ ] Tests: `shellcheck install/install.sh` passes. The negative checks pass on the test host.

## Out of scope
- Tarball download and checksum, key volume, upgrades, backups (full installer comes later).

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

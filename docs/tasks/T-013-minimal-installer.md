# T-013 – Minimal installer for the test host

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** user approval; needs the test host   · **Parallel with:** host track: before T-012
- **Status:** proposed · **blocked** until `pl-lead` confirms the Proxmox test host (D-041)

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

## To verify on host (offline part; tested only against fake commands in `install/testdata/fakepve`)
Risk: **[high]** first.
1. **[high]** `Pool.Audit` on `/pool/plumb` is needed to list pool members (T-012 item 2), and is enough; `Pool.Allocate` is not needed.
2. **[high]** `cicustom vendor=plumb-snippets:snippets/plumb-vendor.yaml` works with only `Datastore.Audit` on `plumb-snippets`; which right `smbios1` needs (`VM.Config.Options`?). Record both in the Result (card criterion).
3. **[high]** The negative checks of the card, each with the token: no upload/change in `plumb-snippets`; no `cicustom` to another storage; no `hookscript`; no change of a guest outside the pool; no VM in another pool; no foreign disk attach; no `import-from` of a foreign volume; no permission change.
4. **[high]** `pveum acl modify … --tokens 'plumb@pve!plumb'` and the same for `--users` give the token exactly the listed rights (privsep intersection).
5. **[high]** The ACL path for a local bridge is `/sdn/zones/localnetwork/<bridge>` with `SDN.Use` (PVE 9.1).
6. **[medium]** `pveum user token add … --output-format json` prints `"value"` once; `pveum role modify --privs` replaces (does not append).
7. **[medium]** `pvesm add dir plumb-snippets --content snippets` creates `<dir>/snippets/`; the dir storage stays root-owned and the snippet 0644 root.
8. **[medium]** `pvesm set <images> --content <old>,import` keeps the old types; `pvesh get /storage/<id>` returns `content` as a comma list.
9. **[medium]** `pveam available --section system` lists `debian-13-standard_*`; the column layout matches the awk; `pveam list` exits 0 on an empty list (the script tolerates both).
10. **[medium]** An unprivileged Debian 13 LXC needs `nesting=1` for systemd (PVE default in the UI); `pct push --perms/--user/--group` and `pct pull` work as used.
11. **[medium]** `pvesh get /cluster/nextid` and `/pools --poolid` output formats as parsed.
12. **[medium]** The snippet install command works on stock Debian 13 and Ubuntu 26.04 cloud images (curl or wget present; `systemctl is-active` before network).
13. **[low]** `pveversion` output format `pve-manager/X.Y.Z/…`; `hostname -s` is the node name; `ip -4 -o addr show dev vmbr0` gives the host address the LXC can reach.
14. **[low]** `plumb serve` in the LXC reads the `PLUMB_PVE_*` settings — needs the serve wiring after T-012 merges; until then the service stops with the clear "installer configuration" error and the install ends without the password line.
15. **[low]** `--remove` leaves nothing behind (`pct destroy --purge 1`, user delete removes its ACLs and token).

## Result (owner, max 8 lines)
`pl-backend/T-013-minimal-installer` (offline part only) · tests: `make lint test build` → pass; `go test ./install` runs the script against fake Proxmox commands.
- `install/install.sh`: install/repair, `--snippet-only`, `--remove`, `--dry-run`; rights per path (D-081), snippet (D-082), LXC and token handling (D-083), behaviour (D-084).
- Tests: dry run changes nothing and prints every change; exact rights, no forbidden right; PVE < 9.1 refused before any change; root and confirmation needed; install keeps the token out of all output and writes it only to the 0600 env file; a second run creates nothing again; remove needs confirmation and refuses with guests; snippet is valid YAML/JSON/sh with strict URL check.
- `make lint-sh` (bash -n + shellcheck; CI enforces shellcheck, the dev VM has none).
- **Left for the host:** the 15 items above, the negative checks (`make test-pve`), the Result facts on `Pool.Audit`, `cicustom`, `smbios1`; serve wiring of `PLUMB_PVE_*` after T-012.

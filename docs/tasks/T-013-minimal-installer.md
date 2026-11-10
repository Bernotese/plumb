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
- [ ] `install/install.sh` creates: the six roles of D-081, user `plumb@pve`, token with privilege separation, pool, storage `plumb-snippets` (content `snippets` only, root-owned), storage `plumb-images` (content `import` only), the static vendor snippet, an unprivileged Debian LXC without any host mount, a systemd service.
- [ ] The snippet holds the Plumb CA, the agent URL and an idempotent install command. It holds no secret. The install command works on stock Debian 13 and Ubuntu 26.04 cloud images without extra packages.
- [ ] `install.sh --snippet-only --ca <file> --url <url>` writes only the snippet (for a controller that runs on the dev VM).
- [ ] The roles have exactly the privileges of ARCHITECTURE §7 and D-081. The script prints them before it changes anything. It copies `pve-root-ca.pem` into the LXC.
- [ ] The token is stored only inside the LXC (mode 0600). The script never prints it.
- [ ] The script is idempotent. `install.sh --remove` removes everything it created, after a confirmation. It ends with the UI address and the initial login.
- [ ] Negative checks with the token (in `make test-pve`): it cannot upload or change a file in `plumb-snippets`; cannot set `cicustom` to a volume of another storage; cannot set `hookscript`; cannot change a guest outside the pool; cannot create a VM in another pool; cannot attach a disk of a foreign VM; cannot `import-from` a foreign volume; cannot change permissions.
- [ ] Facts to record in the Result: is `Pool.Audit` needed (never `Pool.Allocate`); which rights `cicustom` and `smbios1` need.
- [ ] Tests: `shellcheck install/install.sh` passes. The negative checks pass on the test host.

## Out of scope
- Tarball download and checksum, key volume, upgrades, backups (full installer comes later).

## Follow-up (pl-lead, 2026-11-10)
- Slice 2 (D-086, PR #32): only `plumb key init` creates the master key and shows the recovery key, on a terminal. `plumb serve` never creates it. When the secret store (T-028) is on `main`, the installer must create the key volume and call `plumb key init` with a real TTY (`pct enter` or `lxc-attach`, not `pct exec`). This needs its own card and a review; it is not part of the minimal installer.
- Where it goes in `install/install.sh` (pl-backend, T-041; not implemented):
  1. `ensure_lxc`, `pct create` (~l. 615): add the key volume, e.g. `--mp0 "$DISK_STORAGE:1,mp=/var/lib/plumb/key,backup=0"`; drop the comment "no mount point".
  2. `setup_lxc` (~l. 699): after `install -d … /var/lib/plumb`, give the mount point `/var/lib/plumb/key` to `plumb`, mode 0700 (D-102: `key init` never creates the directory).
  3. `setup_lxc`, before `systemctl restart plumb.service` (~l. 711): if `/var/lib/plumb/key/master.key` is missing, run `plumb key init --data /var/lib/plumb` as user `plumb` on a real TTY (`lxc-attach`/`pct enter`, not `pct exec`). A repair run with a key skips it. `--dry-run` only prints it. Without a terminal the installer stops before the start.
  4. `--remove`: `pct destroy --purge` also destroys the key volume; the confirmation text must say that the database is unreadable without the recovery key.

- Agent unit in the cloud-init snippet (from the PR #72 review, D-161): the agent refuses a state directory with a mode other than 0700. Today the unit has no `StateDirectory=` and the agent creates its default directory with 0700 itself; keep it so. If a `StateDirectory=` is ever added, it needs `StateDirectoryMode=0700`, else the agent refuses every start. Check on the real host that the agent starts in a fresh VM.

## Plan (architect, optional)

## To verify on host (offline part; tested only against fake commands in `install/testdata/fakepve`)
Risk: **[high]** first (items 6, 15 raised and 16–18 added after review).
1. **[high]** `Pool.Audit` on `/pool/plumb` is needed to list pool members (T-012 item 2), and is enough; `Pool.Allocate` is not needed.
2. **[high]** `cicustom vendor=plumb-snippets:snippets/plumb-vendor.yaml` works with only `Datastore.AllocateSpace` on `plumb-snippets` (D-081); which right `smbios1` needs (`VM.Config.Options`?). Record both in the Result (card criterion).
3. **[high]** The negative checks of the card, each with the token: no upload/change in `plumb-snippets`; no `cicustom` to another storage; no `hookscript`; no change of a guest outside the pool; no VM in another pool; no foreign disk attach; no `import-from` of a foreign volume; no permission change.
4. **[high]** `pveum acl modify … --tokens 'plumb@pve!plumb'` and the same for `--users` give the token exactly the listed rights (privsep intersection).
5. **[high]** The ACL path for a local bridge is `/sdn/zones/localnetwork/<bridge>` with `SDN.Use` (PVE 9.1).
6. **[high]** `pveum user token add … --output-format json` prints `"value"` once; `pveum role modify --privs` replaces (does not append).
7. **[medium]** `pvesm add dir plumb-snippets --content snippets` creates `<dir>/snippets/`; the dir storage stays root-owned and the snippet 0644 root.
8. **[medium]** `pvesh get /storage/<id>` returns `content` as a comma list; `pvesm add dir plumb-images --content import` works.
9. **[medium]** `pveam available --section system` lists `debian-13-standard_*`; the column layout matches the awk; `pveam list` exits 0 on an empty list (the script tolerates both).
10. **[medium]** An unprivileged Debian 13 LXC needs `nesting=1` for systemd (PVE default in the UI); `pct push --perms/--user/--group` and `pct pull` work as used.
11. **[medium]** `pvesh get /cluster/nextid` and `/pools --poolid` output formats as parsed.
12. **[medium]** The snippet install command works on stock Debian 13 and Ubuntu 26.04 cloud images (curl or wget present; `systemctl is-active` before network).
13. **[low]** `pveversion` output format `pve-manager/X.Y.Z/…`; `hostname -s` is the node name; `ip -4 -o addr show dev vmbr0` gives the host address the LXC can reach.
14. **[low]** `plumb serve` in the LXC reads the `PLUMB_PVE_*` settings — needs the serve wiring after T-012 merges; until then the service stops with the clear "installer configuration" error and the install ends without the password line.
15. **[high]** `--remove` leaves no ACL, token, role, storage or LXC behind (`pct destroy --purge 1`; user delete removes its ACLs and token).
16. **[high]** Run the negative checks also on a host where `local` has `snippets` content and where an old D-032 role `Plumb` exists (the installer must refuse or remove them).
17. **[high]** The token cannot create a disk on `plumb-snippets`; cannot set `cicustom` to a snippet on the disk storage or on `local`; cannot upload over a file on `local`.
18. **[high]** `FetchImage` and `Create` of T-012 pass without `Sys.Audit` and `VM.Config.CDROM` (D-081).
19. **[medium]** `dpkg-query -W -f '${Version}'` and `dpkg --compare-versions` work as used for the version floor; `pct config` shows the `description` with the marker `plumb-installer-lxc` and `hostname:` as parsed.
20. **[medium]** `pveum acl list --output-format json` gives `path`, `ugid`, `roleid`, `type`; `pveum acl delete` removes one entry; `pveum role list` gives `privs` as parsed.

## Result (owner, max 8 lines)
`pl-backend/T-013-minimal-installer` (offline part only) · tests: `make lint test build` → pass; `go test ./install` runs the script against fake Proxmox commands.
- `install/install.sh`: install/repair, `--snippet-only`, `--remove`, `--dry-run`; rights per path (D-081), snippet (D-082), LXC and token handling (D-083), behaviour (D-084).
- Tests: dry run changes nothing and prints every change; exact rights, no forbidden right; PVE < 9.1 refused before any change; root and confirmation needed; install keeps the token out of all output and writes it only to the 0600 env file; a second run creates nothing again; remove needs confirmation and refuses with guests; snippet is valid YAML/JSON/sh with strict URL check.
- `make lint-sh` (bash -n + shellcheck; CI enforces shellcheck, the dev VM has none).
- Review fixes: remove deletes only its own files (state file), LXC marker check, pool guard fails closed, stray ACLs removed, role rights checked, token tied to a live LXC, temp-file trap, restart, PEM charset, download timeouts, first IPv4, test hooks documented; D-081 rule of pl-architect (roles, `plumb-images`, storage checks, version floor).
- **Left for the host:** the 15 items above, the negative checks (`make test-pve`), the Result facts on `Pool.Audit`, `cicustom`, `smbios1`; serve wiring of `PLUMB_PVE_*` after T-012.

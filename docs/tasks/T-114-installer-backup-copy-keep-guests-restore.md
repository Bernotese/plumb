# T-114 – Installer: backup copy timer, `--remove --keep-guests`, `--restore`

- **Owner:** pl-reviewer   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-110   · **Parent:** T-107 (C4)

## Goal
The installer sets up the off-host copy and can replace the controller while the guests stay.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C4, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-114.md`; ask pl-lead for numbers

## Acceptance criteria
- [x] `--backup-storage <storage>`: host timer per D-289 (only the encrypted file; pointer, name regex, size cap, temp + rename, storage active and mounted, root-owned status dir; no key on the host; no token right)
- [x] `--remove --keep-guests`: fresh backup copied off and recovery key checked against the header fingerprint before any removal; keeps pool, guests, templates, user, roles, storages, `plumb-snippets`
- [x] `--restore <file>`: recovery key on a TTY, same address, no DHCP, pool/node/install id check, new token with its ACLs
- [x] Dry run and repair run of each path; fake-PVE and shell tests; shellcheck clean. No host run in this card (T-122 does it)

## Out of scope
- Any new token right; a change to `local` or `local-lvm`
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
pl-reviewer/T-114-installer · tests: `make lint test build` → green; `install/backup_test.go` (11 tests against fakepve), shellcheck clean.
- Copy timer: generated `plumb-backup-copy` + conf + units, storage type/active/mounted checks, reads by `dd iflag=nofollow` with limits, magic + size + sha256, host-built name, retention, status in a root-owned dir (D-312).
- `--remove --keep-guests`: backup now → copy (storage or `--backup-to`) → host verify → only then removal; kept state (D-313).
- `--restore`: header checks, host verify first, new token with ACLs, `plumb key restore` + `plumb restore` on the TTY (D-314).
- Needs from T-112/T-113: `plumb backup now --data DIR` (waits, exit 0), `plumb backup verify FILE` (exit 3 wrong key), `plumb key restore`, `plumb restore --data --from`; env `PLUMB_BACKUP_TIME`/`_KEEP`/`PLUMB_INSTALL_ID`. No host run (T-122).

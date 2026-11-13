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
- [ ] `--backup-storage <storage>`: host timer per D-289 (only the encrypted file; pointer, name regex, size cap, temp + rename, storage active and mounted, root-owned status dir; no key on the host; no token right)
- [ ] `--remove --keep-guests`: fresh backup copied off and recovery key checked against the header fingerprint before any removal; keeps pool, guests, templates, user, roles, storages, `plumb-snippets`
- [ ] `--restore <file>`: recovery key on a TTY, same address, no DHCP, pool/node/install id check, new token with its ACLs
- [ ] Dry run and repair run of each path; fake-PVE and shell tests; shellcheck clean. No host run in this card (T-122 does it)

## Out of scope
- Any new token right; a change to `local` or `local-lvm`
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-135 – README: backup and restore (after the slice 4 host proof)

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-122 (done), T-133 (PR #234)

## Goal
The README section "Backup and restore" says what Plumb does since slice 4, for users only.

## Context (read only these)
- `README.md`; `install/install.sh --help`; `plumb backup --help`, `plumb restore --help`; `docs/tasks/T-122.log.md` (what was proven on a real host); `docs/ARCHITECTURE.md` §16 for the facts only

## Acceptance criteria
- [ ] Content: what a backup holds and what not (not the Proxmox token secret, not the master key; the guests themselves are not in it) · daily, keep 7 · the off-host copy with `--backup-storage` (name a storage on another device; a directory on the same host is not an off-host copy) · check a copy with `plumb backup verify` now and then: only a restore test proves a copy · replace the controller: `--remove --keep-guests`, then `--restore` (same address; recovery key needed; a wrong key stops before any change) · after a restore: restored mode, new admin password shown once, tokens disabled until a person enables them, confirm only when the old controller is off; guests made or deleted after the backup are shown and never touched
- [ ] Short notes in the existing sections: stop/start, tags, disk grow (grow only), the `firewall` Block (SSH stays open), "Accept" Plans need a person
- [ ] Known limits updated (a restored Plumb needs the same address; one admin user only; `network` and more NICs later)
- [ ] Every command and flag exists on main; no development docs; no addresses, tokens or host data; at most one more picture (Settings → Backups, dev demo, same rules as T-131)
- [ ] `make lint` green

## Result (owner, max 8 lines)
branch · commit · tests · notes

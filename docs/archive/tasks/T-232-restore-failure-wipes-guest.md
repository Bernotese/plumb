# T-232 – A failed restore must not leave the guest empty without a way back (host finding F2)

- **Owner:** pl-backend (author of T-222)   · **Reviewer:** pl-reviewer (critical) + pl-architect (the design point)   · **QA:** –
- **Size:** M   · **Depends on:** –

## Goal
Found on the real host (T-225 part 1, F2): a restore over a VM whose archive is damaged wipes the guest. PVE's restore with `force` first removes the old disks, then the extraction fails ("Restored data doesn't match checksum") and PVE removes the new volumes too: the VM is left with a config of one line and no disk. Plumb's Run then says "the way back: re-enroll the Target", which is wrong: there is nothing to start. Any restore failure (damaged archive, full storage, I/O error) is a total loss of the guest, and Plumb does not say so. Small: the step output carries raw PVE task text with host paths.

## Decision of pl-lead (write it as D-420 in `docs/decisions/T-232.md`; pl-architect may object in the review)
1. **Safety backup first.** A restore Run starts with a backup of the guest as it is now (kind `pre-restore`, marked, own lineage rules, not counted against the manual cap, kept until the next successful restore or until a person deletes it). If this backup fails (storage full, error), the Run stops **before** anything is stopped, revoked or removed. The Plan shows the step and the space it needs (the free-space gate applies).
2. **Honest failure.** When the restore call fails after PVE began, the Run says: the guest may have no disk now; the way back is to restore the `pre-restore` backup (named) or another backup; re-enroll only after a restore that worked. The Target gets a clear state (an Issue `guest:restore-failed` or the existing `guest:missing` shape; choose and say why), and "restore" of the safety backup is offered at once.
3. **Plan text.** The restore Plan says in plain words: PVE removes the guest's disks before it writes the backup; if the restore fails, the guest is empty; Plumb takes a safety backup first.
4. The person may skip the safety backup only for a guest that is already broken (no disk, or the last restore failed), with an explicit tick in the wizard, human-only like the restore itself.
5. Step outputs never carry raw PVE task text: only Plumb's own text plus the PVE task id; host paths stay out (also check the backup job and the other Run steps of slice 7 for the same leak).

## Acceptance criteria
- [ ] The five points above. No new right (the safety backup uses `VM.Backup`; the restore of it uses what D-380 gave).
- [ ] Tests with the fake driver for each failure point: safety backup fails → nothing changed; restore fails after the disks are gone → state + text + the offered way back; restore of the safety backup works and re-enrolls; a second failure in a row.
- [ ] Update the web wizard texts (pl-frontend does that part in a tiny PR after yours; tell him the fields) and ARCH §20.
- [ ] `make lint test`; PR to `main`; report. Proven on the host in T-225 with the damaged archive again (the wiped VM 9002 stays as evidence until then).

## Result (owner, max 8 lines)
branch · commit · tests · notes

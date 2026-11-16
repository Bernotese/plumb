# T-253 – Small fixes from the slice 8 host acceptance (T-247)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-247 (add each low it finds here; one PR at its end)

## Goal
Lows that the host acceptance of slice 8 finds. Text and small behaviour fixes only; anything bigger gets its own card.

## Acceptance criteria
- [ ] F3 (step V): when the free of a removed disk fails with PVE's "still in use", Plumb's step text and the `guest:volume-left` way out say "a snapshot may hold the volume: remove the snapshot first, then `qm/pct set <id> --delete unusedN`; do not `pvesm free` a volume that an unusedN line names". PVE's own text stays fenced/cut as today (no guest text).
- [ ] Step 3b: the `not-restorable` text names only `--extra-vlan vmbr9:20`, but the repair run also needs `--extra-bridge vmbr9` → name every flag the bridge/tag needs.
- [ ] Step 3c: PVE refuses a restore with a bridge/tag that is not allowed BEFORE it removes the disk, for an LXC too (403 `SDN.Use` as the task's end, rootfs volume the same before and after). So 'not started' may be said for a container on the same strict proof as for a VM (the exact exit status + the volumes read back equal); `pl-reviewer` reviews it as critical.
- [ ] Step 9: while a new re-enroll Plan is pending, the Issue's note names that Plan, not an older stale one (the newest Plan that can still run wins; else the newest that cannot).
- [ ] Step 4: a restore Run that a controller stop cut after 'restored' shows the Run as 'failed' although its only step is 'applied' and the follow-up came at the next start (D-427). The Run's state and text must not contradict the step: say what is true (the restore worked; the Run was cut; the follow-up was made at the start) without telling a person to restore again.
- [ ] (more lows of T-247 are added here by `pl-lead`)
- [ ] Tests for each text; `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

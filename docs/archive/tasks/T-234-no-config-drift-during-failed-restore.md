# T-234 – A failed restore must not offer "Revert" of the empty guest (host finding F3)

- **Owner:** pl-architect (pl-backend, the author of T-222/T-232, is on the host)   · **Reviewer:** pl-reviewer (critical: a destructive wrong way back)   · **QA:** –
- **Size:** S–M   · **Depends on:** –

## Goal
Found on the real host (T-225 re-run): after a failed restore the guest is empty for a while. The drift observer saw that empty guest and opened a `guest:config` Issue (image ''→…, memory, disk 0→10, bridge, nic1) with the actions revert / ignore once / ignore by rule. "Revert" there is a destructive re-create from the image, offered next to the right way back (restore of the safety backup). After the safety backup was restored and the agent re-enrolled, `guest:restore-failed` was resolved, but the `guest:config` Issue stayed open (phase drifted) and still offered Revert.

## Acceptance criteria
- [ ] While a restore Run of a Target runs, and while `guest:restore-failed` is open for it: the guest poll opens no `guest:config` / `guest:missing` Issue for that Target (the state is known and named by the restore Issue), and no Plan for that Target can be proposed or approved except a restore (of the safety backup or another backup) and, human-only, a delete: Revert, a param change, a re-create are refused with a text that names the restore Issue. Tokens too.
- [ ] A `guest:config` Issue that is already open when the restore fails gets no actions while `guest:restore-failed` is open (say how: hidden actions + a note).
- [ ] After a restore that worked, the next poll compares again; an Issue that only showed the empty guest is resolved by `system` with a comment; real drift of the restored guest (an older config from the backup) opens a normal Issue.
- [ ] Tests for the host sequence (failed restore → poll → no config Issue / no Revert → restore of the safety backup → clean) and for each refused Plan kind; mutants.
- [ ] `make lint test`; PR to `main`; report. Proven on the host in T-225 (Issue 52 there stays as evidence until the fixed build runs).

## Result (owner, max 8 lines)
branch · commit · tests · notes

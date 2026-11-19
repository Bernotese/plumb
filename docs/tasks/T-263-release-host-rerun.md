# T-263 – Full host re-run on pve-dev with an RC tarball (release slice C7)

- **Owner:** pl-backend (the only agent on the host for this card)   · **Reviewer:** pl-reviewer (the run plan, then the log)   · **QA:** –
- **Size:** L   · **Depends on:** T-259 (#390), T-261 PR A (#391), T-262 (#393) merged; **U12** only for part (a)

## Goal
Release slice, card **C7** of `docs/plans/release-slice.md` (section 3 rows "Upgrade test" and "Release = the tested RC", section 5 row C7, section 5a "P1", row O10). The release candidate is proven on the real host: install from the tarball, upgrade, every slice's acceptance list, and the restore of Plumb itself (R1).

## Rules (hard)
- `AGENTS.md` "Test host" in full. Only what this card lists; another action: ask `pl-lead` first. Every installer run (update, remove, clean install, repair): `--dry-run` → the result to `pl-lead` → the go → the real run. Announce every controller stop before it.
- RCs are built **locally** with `scripts/package-release.sh` from a commit of `main`, never with the workflow; **no tag, no GitHub release or draft, no push of an RC anywhere**. Record the commit and the four hashes.
- No new right, no change of the network, of `vmbr0`, of `local` / `local-lvm` definitions, of the SSH setup. `vmbr9` stays as it is (its undo is card C8). `storage.cfg` copy at the start, byte-equal at the end; the ACL view at the start = at the end.
- Real `plumb key init` runs are allowed on this host (D-180); the recovery key goes to `~/.config/plump/testhost-recovery.key` (0600), only "key stored in <path>" in the log. Tokens by a 0600 file or stdin, never on a command line. No address, token id, MAC, key, fingerprint or local time in the log or the PR.
- Test objects only in the Plumb pool, named `t263-*`; cleanup at the end with the go of `pl-lead`; test secret files deleted after it (D-416).

## Steps
- [ ] **First the run plan** as `docs/tasks/T-263.log.md` on a branch (ordered steps, the proof per step, the objects, each installer line without secrets, the root actions you will ask for, the cleanup list). `pl-reviewer` checks it; `pl-lead` says go. No host action before that.
- [ ] RC1 from the then-current `main`; the installer's bundle check on the host (also once with a damaged file and once with a `PLUMB_INSTALL_*` variable set: refused, nothing changed).
- [ ] Upgrade (b): the running controller → RC1 → RC2 (RC2 = RC1 plus a commit, or a rebuilt later `main`): 0 `pveum` lines, data kept, the version line old → new.
- [ ] Every slice's acceptance list in short form (one Target per kind is enough where the slice lists say so): VM and LXC from an Image, Blocks and drift, the firewall and the network Target on `vmbr9`, a guest with a data disk, guest backup and restore, the damaged-input restores, MCP `explain_issue`, repair and adoption.
- [ ] **R1** exactly as section 5a P1 (backup → `--remove --keep-guests` → clean install from the tarball → `plumb restore` + confirm) with its seven acceptance points.
- [ ] Upgrade (a) from the user's own version: **only when `pl-lead` gives the version (U12)**. Plan it as a detachable last part; if the answer is not there at the end, stop before it and report.
- [ ] End proof (host clean as after T-247), the leak grep of the log (counts only), the log as a PR.

## Result (owner, max 8 lines)
branch · commit · tests · notes

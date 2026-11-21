# STATUS

What is open now, and where things are. History: `docs/archive/STATUS-history.md`; what a user gets: `CHANGELOG.md`.

## Now: the cleanup before the first release (D-442, user order)

Slices 1 to 8 are built, reviewed, tested and accepted on the real test host. The release slice is done up to the points that need the user. Proof row by row: `docs/plans/finished-check.md`.

The user asked for one cleanup and check before the release (order file `~/workspcae/plump-findings/CLEANUP-BEFORE-RELEASE.md`). The code freeze on `main` is lifted for this work only.

| Point                        | Card  | Owner                | State                                                                                                                                                                                                                                                   |
| ---------------------------- | ----- | -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0 is everything finished?    | T-270 | pl-reviewer          | done (#412)                                                                                                                                                                                                                                             |
| 1 branches                   | –     | pl-lead              | done (2026-11-20): the 13 branches without a PR, the leftovers and the loose files are gone (Hermes, on the user's order). pl-lead writes each merged head branch to `~/workspcae/plump-findings/BRANCHES-TO-DELETE-2026-11-20.md`; Hermes deletes them |
| 2 docs: keep or archive      | T-272 | pl-reviewer          | done (#414): 301 task files moved to `docs/archive/tasks/`, 36 unused pictures deleted                                                                                                                                                                  |
| 3 context files short        | T-273 | pl-lead, pl-reviewer | done (#416; the tables of changed and replaced decisions are in `docs/DECISIONS-index.md`)                                                                                                                                                              |
| 4 dependencies and dead code | T-271 | pl-backend           | done (#411 dead Go code, #415 the installer end-lines test; modules, packages, scripts: nothing to remove)                                                                                                                                              |
| 5 English and one vocabulary | T-274 | pl-backend           | done (#413 web texts, #417 Go texts, #420 openapi and ARCHITECTURE terms)                                                                                                                                                                               |
| 6 README final               | T-275 | pl-backend           | done (#422): 819 → 566 lines + `docs/guide/`, 18 new pictures; no decision number in any user-visible text (#421, #423)                                                                                                                                 |

**All six points are done (2026-11-20).** The RC5 host run (T-277) found a product fault before the upgrade step: a container can miss its agent for ever by a first-boot race of two units (in RC4 too, stock and built templates); fixed by T-278 (#425). The host run with **RC5** passed (log `docs/tasks/T-277.log.md`; 11 points that were test-only are proven on the host now: `docs/plans/finished-check.md` section 2). Its small findings are fixed by T-279 (#429). The short host check with **RC6** passed too (log PR #430): the upgrade RC5 → RC6 with 0 rights changes, the built-container path, the fixes seen on the real controller, no secret in the tarball or the host data. **The cleanup order (D-442) is done; the release questions are with the user again (2026-11-20)** on `pve-dev` (the upgrade RC4 → RC5, the checks the changes touch, and a compact pass of what was never run on the host: `docs/plans/finished-check.md` section 2). Then one short message to the user with the release questions.

The tested candidate is **RC6 = `3eab710`** (built locally, no tag; host logs `docs/tasks/T-277.log.md` and `docs/tasks/T-279.log.md`; leak hunt `docs/tasks/T-266.log.md`: no secret). **Code freeze on `main` again until the release:** only docs, logs and plans merge. The expected hashes of a `0.1.0` build of this commit are in the T-279 log (R6-0).

## Open for the user

- **The user said yes to the release (D-445, 2026-11-21):** v0.1.0, the attestation, pl-lead runs the workflow to a draft, **the user publishes**; U9 (b) and U10 (no new right) as the README says today. pl-lead works through `docs/plans/release-day.md` up to the draft.
- **Answered 2026-11-21 (D-444):** U11 nothing open under secret scanning. U12: the user has no installation of his own, so the upgrade test from his version, U7 and U8 are dropped; his first install will be made fresh from the release. `vmbr9` may be removed (T-280). `testhost-api.token` is deleted.

## Drafts that wait

- For the release: #392 (attestation in `release.yml`, approved; merges after the yes to U6), #398 (release notes), #407 (release-day checklist), #405 (becomes the notes for the user's first install from the release: no repair run is needed).
- For after the release: #408, #409 (two small cards of `docs/plans/after-release-slice.md`).

## Open technical points

- **`TestGuestFirewall` (O1):** failed twice (2026-11-14, 2026-11-16); the cause is not found. A probe loop runs in every guest CI job (4000 of 4000 rounds clean on 2026-11-19). Read the dump at the next failure; never just rerun.
- **Guest tests and the outside network:** fixed by T-276 (#419): no test waits for a package mirror; the NIC test measures the main address from before the apply (24 ms gap, a refreshed DHCP lease with the same address: D-443).
- **Proven by tests only, never on the host:** the list is `docs/plans/finished-check.md` section 2 (the web UI as a whole, rollback, import, the one-time-password unlock of other users after a restore, PBS, …). The RC5 host run takes what it can.
- **Test host `pve-dev`:** only LXC 100 (the controller on RC6), all stores empty, 9 ACL lines; the test bridge `vmbr9` is removed (T-280, 2026-11-21: the network file is byte for byte the state before T-224).

## Where things are

- Product and rules: `docs/BRIEF.md`, `docs/ARCHITECTURE.md`, `docs/DECISIONS.md` (the last number is in its header).
- Plans: `docs/plans/ROADMAP.md`, `docs/plans/release-slice.md`, `docs/plans/after-release-slice.md`, `docs/plans/finished-check.md`.
- Cards and logs: `docs/tasks/` (open cards, host logs, leak hunt logs); everything older: `docs/archive/`.
- Release: `docs/RELEASE.md`, `CHANGELOG.md`.
- Test host access and rules: `AGENTS.md` "Test host".

## Dev VM notes

- Guest tests need the `docker` group: `sg docker -c "make test-guest"` in an old session.
- A current GitHub CLI (with `gh attestation`) is at `~/.local/opt/gh-new/gh_2.105.0_linux_amd64/bin/gh`; the system `gh` is older.

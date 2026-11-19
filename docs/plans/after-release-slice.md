# Plan draft: the first slice after the release (from `after-release.md`)

**Status:** a first draft by `pl-backend` for `pl-lead`, written during the code freeze (D-326 point 3). **Nothing here is built.** The cards are proposals: size, risk and order are the team's recommendation. `pl-reviewer` checks the plan before any card starts.

**Sources:** `docs/plans/after-release.md` (the three backlog rows and O9 (a)); the lows on record in `docs/STATUS.md` ("Open technical points"); the T-263 log (the host re-run of the release candidates); the review notes of #394, #396 and #402.

**Rules that hold for every card** (AGENTS.md, D-438):

- One card after the other.
- The restore path, sessions, rights and the installer are critical: `pl-reviewer` reviews them (or `pl-backend` when `pl-reviewer` wrote the card).
- A change of the restore path needs a host proof on `pve-dev` (a short run: dry run → `pl-lead` → go).
- The README states only what a test or the host proved.

## The cards

| #   | Card                                                                           | From                                                                                            | Size                    | Risk                                                  | Review                                                                                                                                | Host proof                                                                                             |
| --- | ------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------- | ----------------------- | ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| 1   | **O9 (a): the drop of older safety backups after a cut restore Run**           | `after-release.md` row 4; D-429; `internal/api/restore_cut_drop_test.go` pins today's behaviour | S                       | low–medium (the restore follow-up)                    | critical (restore path)                                                                                                               | yes, short: a restore cut by a controller stop, then the next start drops the older safety backups     |
| 2   | **"Restore call begins" mark**                                                 | `after-release.md` row 2 (#374 L2, T-260 row 69)                                                | S                       | medium (the restore step and its text)                | critical                                                                                                                              | yes, short: a stop between the revoke and the restore call → the Issue does not say "may have no disk" |
| 3   | **`keyAttr` from `resourceKinds.json`**                                        | `after-release.md` row 1 (#361 L2, row 35)                                                      | S                       | low (web only; a test catches a drift today)          | normal (`pl-reviewer`)                                                                                                                | no                                                                                                     |
| 4   | **A re-enroll Plan survives a head move**                                      | `after-release.md` row 3 (#382 O9 (b), row 76)                                                  | M                       | **high**: it touches the staleness rule of every Plan | critical; **a plan first** (the comparison of the rendered documents at the base and at the head, and why no other origin may use it) | yes: the T-247 F4 case (two restores, approve one re-enroll → the second is not stale)                 |
| 5   | **`npm audit` check again** (`deepmerge` 4.3.1, GHSA-gcqx-w48m-w2h4, dev only) | STATUS (T-155 "check again in slice 9"); the release notes "Known limits"                       | XS                      | none                                                  | `pl-codex-fix` or `pl-reviewer`                                                                                                       | no                                                                                                     |
| 6   | **The users' unlock by a one-time password on a real host**                    | T-263 L1 of #404: proven only by `TestResetUserPassword`, never run on the host                 | XS (host only, no code) | none                                                  | —                                                                                                                                     | yes: in the next host run with a restore, unlock one user and log in                                   |

**Watch items** (no card until something new happens):

- **O1:** the unexplained `TestGuestFirewall` failure. The probe loop runs in every guest CI job (4000 of 4000 rounds connected by 2026-11-19). The next failure brings its dump; a card starts from that dump.
- **#402 notes (T-269):**
  - an install from before T-269 prints `<UNKNOWN …>` until its first upgrade or repair (the README says so);
  - the guard of the copy timer stops late, after the LXC exists (a code-error guard only).

**Housekeeping** (`pl-lead`): the STATUS line "Flaky test, seen once … Card T-251" is stale: T-251 is merged (#379).

## Order and why

1. **Card 1 (O9 (a))** first: small, in the restore code that T-263 just proved on the host. Its test already pins today's behaviour, so the change is a test flip plus the fix. The README line about it goes away.
2. **Card 2 (the restore mark)**: also the restore path. One host run can prove cards 1 and 2 together.
3. **Card 5 (`npm audit`)**: a five-minute check between the critical cards. If a fixed `deepmerge` exists, the dependency is bumped and the release notes line goes.
4. **Card 3 (`keyAttr`)**: a web refactor with no user-visible change; low risk, a good card between reviews.
5. **Card 4 (the re-enroll Plan)**: last, and only after its plan is reviewed. It is the only high-risk card, and only a comfort today: the Issue already offers the way out (D-440).
6. **Card 6** rides with the next host run that has a restore (with card 1 or 2, or card 4).

**The team's recommendation:** cards 1, 2, 3 and 5 form the slice; card 4 gets its plan in that slice and is built in the next one, if `pl-lead` still wants it after the plan.

## Open questions for `pl-lead`

- Card 4: worth its risk at all, given D-440's way out? The plan would answer the cost; the recommendation is "plan yes, build only if the plan shows a narrow rule".
- The `vmbr9` undo on the test host and step 7 of T-263 (U12) belong to the release slice, not here.

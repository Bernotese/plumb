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

## Plan: card 2, the "restore call begins" mark

**The fault** (#374 L2, T-260 row 69). The start rule `recoverRestores` (D-427) knows only two facts about a cut restore Run:

- the open `guest:restore-failed` Issue in the "running:" state, written by `restoreRunningIssue` before anything changes;
- the step output with the `restored ` mark, written right after the Proxmox restore call worked.

Without the mark it says "failed: the controller stopped during the restore; **the guest may have no disk**". That is true only once the Proxmox restore task exists. A stop earlier in the Run gets the same text, although no disk was touched.

**The Run today** (`internal/plan/restore_guest.go`, the guest:config step of a restore Plan), in order, with the stop points that matter:

| #   | Step                                                             | What a stop here leaves                            | Today's start rule | Right answer                                                               |
| --- | ---------------------------------------------------------------- | -------------------------------------------------- | ------------------ | -------------------------------------------------------------------------- |
| 1   | stop the guest (`stopGuest`), if it ran                          | the guest stopped or stopping; no Issue            | nothing (no Issue) | as today                                                                   |
| 2   | the safety backup (`SafetyBackup`, D-420)                        | a backup job; no Issue                             | nothing            | as today                                                                   |
| 3   | `restoreRunningIssue` (the Issue "running:", with the way back)  | the Issue, the guest stopped, the credential valid | "may have no disk" | **"did not start; the disks are as before"**                               |
| 4   | `Agents.Revoke`                                                  | the credential revoked                             | "may have no disk" | "did not start; disks as before; **the credential is revoked**: re-enroll" |
| 5   | `DropHostKeys`                                                   | the pins dropped                                   | "may have no disk" | as 4                                                                       |
| 6   | `RestoreGuest`: the driver's checks (the volume's size and time) | no Proxmox task yet                                | "may have no disk" | as 4                                                                       |
| 7   | `RestoreGuest`: the Proxmox task is created and runs             | the disks may be gone                              | "may have no disk" | as today (correct)                                                         |
| 8   | the `restored ` mark                                             | the restored disks                                 | the follow-up      | as today                                                                   |

**The design:**

- **A new step output mark, `restore call `** (a fixed prefix like `restoredMark`), is written with the same `s.set` **right before** `RestoreGuest`, after `DropHostKeys`.
  - **Fail closed:** when the mark cannot be written, the Run stops before the call, with "nothing was restored". It also runs `endRunning()`, revokes nothing more, and starts a guest that ran (the path of `ErrRestoreNotStarted`).
  - Stop points 6–7 then read as "may have no disk". That is safe: the mark means "maybe called".
- **What the start rule reads:**
  - the `restored ` mark → the follow-up (as today);
  - the `restore call ` mark → "failed: the controller stopped during the restore; the guest may have no disk" (as today);
  - **no mark** → a new text in the Issue: "the controller stopped before the restore call: the guest's disks are as before; it is stopped".
    - **The credential:** the start rule reads it from the store (`Agents`: revoked or not). It does not need another mark.
    - **When it is revoked:** the Issue stays open in a "not restored, re-enroll needed" state with the way out "propose the re-enroll" (one re-enroll Plan, as D-440 makes; the disks are the old ones, so a re-enroll gives the old guest its agent back).
    - **When it is not revoked:** the Issue ends (D-428 comment "the restore did not start"), and the gate opens again.
  - **The guest stays stopped** in both no-mark cases; the text says so. Plumb does not start it at start time: that would be a guest change without a Plan.
- **Narrowing step 6** (optional; the recommendation is not in this card): split `RestoreGuest` into a check and a call, so a stop during the driver's checks also reads "did not start". Steps 6–7 are milliseconds apart, and the safe side costs only a too-careful text.

**Tests** (in `internal/api`, with the `cutRestore` helper and the fake restorer):

- a cut with no mark and the credential valid → the Issue ends with "did not start", the gate is open, no re-enroll Plan;
- a cut with no mark and the credential revoked → the Issue in "re-enroll needed" with the way out, exactly one re-enroll Plan after the way out, no "may have no disk";
- a cut with the `restore call ` mark → "may have no disk" (today's text, the way back named);
- a cut with the `restored ` mark → the follow-up, as today;
- the mark write fails (a store error) → the fake records **no** `RestoreGuest` call; the Run says "nothing was restored";
- **mutants:** move the mark after the call (the "may have no disk" test with a cut after the call must fail); drop the credential read (the revoked case must fail).

**Host proof** (`pve-dev`, one short run, dry run → `pl-lead` → go for each installer step; announced controller stops):

- **(a) A stop during the guest stop (step 1) or the safety backup (step 2):** these are windows of seconds. A guest with `allow_restart` and a slow shutdown gives one; `systemctl stop plumb` in the LXC. Then the start rule must leave no Issue, or end it.
- **(b) A stop right at the mark (steps 6–7):** with the T-247 method (an in-LXC read-only trigger on the step output change, a window of about 15 ms). Then "may have no disk", and the way back works.
- **Steps 3–5** are milliseconds wide and only practical in the unit tests; the log says so. Both stops are announced to `pl-lead`; all objects are `t-*` objects in the pool.

**Size and risk:** S–M, risk medium: the restore path, a new Issue state, and the first time the start rule reads the agent store. The review is critical.

## Plan: card 4, a re-enroll Plan that survives a head move

**The fault** (#382 O9 (b), T-247 F4, seen again in T-263 4f). A re-enroll Plan is pinned to the head revision at its making, like every Plan. `ApprovePlan` (`internal/store/plan.go`) marks it stale when the head moved: any approved Plan of any Target moves the head. After two restores, approving the first re-enroll makes the second stale. D-440 handles it: the Issue says "the re-enroll Plan N can no longer run (stale): plan the re-enroll again", and one click makes a new one. So this is a comfort, not a safety fault.

**What "equal documents" would have to mean.** A re-enroll Run writes the whole desired guest config of the Target, makes a new enrollment, and starts the guest. So "the head move does not matter" needs the **rendered input of that Run** to be equal at the Plan's base and at the head:

- the Target document itself (spec, `guest`, `blocks` with their params, `allow_restart`);
- every Block the Target uses, by name (the rendered resources, not only the name);
- the Image document the Target names (the guest spec reads it);
- the **secret values** that the rendered Blocks reference. They are not in the revision; their counters live in the store, so the comparison must cover them, or a changed secret would be sent under an old Plan;
- the Settings and the install values that the render reads (DNS, the vendor snippet). These come from the install, not from the revision; they cannot change by a head move.

The safe form is a byte compare of the canonical render (the same function the Plan shows), at the base and at the head, plus the secret counters. Any error → stale (fail closed).

**What the approve path and the D-439/D-440 gate must re-check** (in the same transaction as the approve, so the head cannot move between the compare and the commit):

- the Target still exists, with the same binding (VMID and `gen`), not deleted and made again;
- its `guest:restore-failed` Issue is still open in the "restored" state, and names **this** restore Run (`restore_run`). A second restore of the same Target makes the old Plan stale;
- no other re-enroll Run of the Target worked in between (the agent is not enrolled already);
- no other Plan of the Target is pending or running (the D-440 one-per-Target rule).

**Where it must still go stale:** a change of the Target document, of any Block it uses, of its Image, of a referenced secret; a delete and re-make of the Target; a second restore; a worked re-enroll by another path; and any compare error.

**The risks:**

- It is the **first exception to "a Plan is pinned to its base"**, the rule that every review since slice 1 relies on.
- The Plan's shown changes stay true only because the render is equal, so a bug in the compare means approving something other than what was shown. That is the class of fault that D-323 and D-441 close elsewhere.
- The secret counters add a second source next to the revision.
- And the gain is one click, in a rare case (two restores at the same time).

**Recommendation: do not build the exception.** If the click must go, there is a narrower way that keeps every Plan pinned:

- **When a re-enroll Plan goes stale** while its Issue is in the "restored" state (the place where D-440 already notices it), **the system proposes a fresh re-enroll Plan for the head** (origin `reenroll`, author `system`; one per Target, as D-440 already allows).
- The Issue names the new Plan, and the person approves it as usual.
- That needs no new staleness rule, no render compare and no secret compare. It needs one test per path (the stale mark at approve, and the head move seen by the Issue note), and the T-247 F4 host case as the proof.
- The re-proposal is size S, risk low–medium (it adds system-made Plans; a loop guard: at most one re-proposal per stale Plan).
- **Decision for `pl-lead`:** keep today's one click (no card), or the re-proposal (S). The exception itself is not recommended.

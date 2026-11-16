# T-241 – Restore follow-ups (D-427 … D-429)

- **Owner:** pl-architect (implementer; moved from pl-backend, who has T-238 and the host cards)   · **Reviewer:** pl-reviewer (critical)   · **QA:** T-245
- **Size:** L   · **Depends on:** T-244 part (3); after T-238 (shared `issue.go`, `restore_guest.go`, fake driver)

## Goal
Slice 8, child card **C5** of T-236. A controller stop during or after a restore leaves a safe, named state; system resolutions carry a comment; safety backups follow the rule of D-429.

## Context (read only these)
- Plan: `docs/tasks/T-236-slice-8-plan.md` section "Plan" (rows, shared-file order, rules), decisions D-421 … D-435 in `docs/DECISIONS.md`, `docs/plans/ROADMAP.md`. Read row C5, the contract parts and decisions it names, and the review comments of PR #359 on it.
- `AGENTS.md`: "Autonomy and release" (D-326), "Flow" step 5 (D-394), "Git" (remove temporary worktrees), "Test host".

## Acceptance criteria
- [ ] Everything row C5 lists; touch only the files that row gives you (test files next to them, single wiring lines, your own planned guards and marks: no question needed, list them in the PR). Another file: ask `pl-lead` first.
- [ ] The 'running' `guest:restore-failed` Issue: at start, a Target with such an Issue and no Run becomes 'failed: stopped during the restore' (gated, T-234); no restored step → no re-enroll. Test the order (Issue before the revoke) and a stop inside the PVE task.
- [ ] D-429 never drops a newer safety backup than the restored one. D-428: every system resolution has a comment.
- [ ] Host finding of T-244: after the restore of an LXC with a hand-made `mpN`, the re-enroll Plan is refused (D-250 'additions outside Plumb') with only a journal ERROR: no Plan, no Issue, the container stays stopped → a refused follow-up must end in a visible state (the restore Issue says why and what to do), never only a log line. Also: two re-enroll Plans in a row, the second goes stale → make one.
- [ ] Host fact of T-244 part 3: a restore of a backup whose NIC bridge/tag is no longer named is refused by PVE BEFORE it removes the disks (403 `SDN.Use`), and `extractconfig` works with the token. So: a pre-check reads the backup's config and refuses the restore before the guest is stopped or anything is revoked when a bridge/tag is not allowed (text: name the bridge, say 'run the installer with it or change the NIC'); the same check protects the restore of the safety backup. And the failure text must not say 'may have no disk' when PVE refused before it began (a PVE answer that proves nothing was changed = not started).
- [ ] Rules: a new command type goes through the real link in a test; every per-Target table says what happens at delete, unbind and VMID reuse; a new operation updates `roleHints.ts`, the tier test and the contract marks; a new attribute gets the guest-value classification test. Name in the report the CI job that proves the card and which of your tests skip locally.
- [ ] No new token right (D-423). Decisions only with numbers from `pl-lead`. Migrations: the next free number at merge time.
- [ ] `make lint test`; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. Any release step. The user's own installation.

## Result (owner, max 8 lines)
branch · commit · tests · notes

## Handover (pl-architect, paused by the budget rule; state of 2026-11-16)
**Done:**
- **Part 1** is PR #369 (branch `pl-architect/T-241-restore-follow-ups`):
  - the `BackupConfig` reader (`internal/driver/proxmox/backup_config.go`, `plan.BackupConfigReader`, the adapter `cmd/plumb/restoreguest.go`), shared with T-238;
  - the network pre-check `checkBackupNetworks` (`internal/plan/restore_precheck.go`, **not wired**; trunks and parse errors refuse);
  - an SDN.Use refusal counts as "not started" only on PVE's exact exit status, for a VM, with its disk volumes read back unchanged (`proxmox/restore_guest.go`);
  - D-429 in `backup.DropSafetyBackups` (drops only older ones).
- **This branch** (`pl-architect/T-241-part2-wip`, based on the part 1 commit 420a49e): commit 51a5f7d. `plan.endRestoreIssue(ctx, target, id, reason)` in `plan/restore.go` sends the unchanged item with `Output` = reason, so `issue.systemReason` comments on the resolution. It is used for `guest:missing` (`plan/guest.go`) and `agent:silent` (`plan/restore.go` `checkSilent`, `plan/plan.go` hello). `TestRestoreConfirmAndIssues` checks the `agent:silent` comment.

**First:** after #369 merges, rebase this branch on `main` (conflicts are unlikely).

**Left (the shared files; they waited for T-238):**
1. **Pre-check wiring** (`internal/plan/restore_guest.go`):
   - In `ProposeRestore`, after `checkRestore`, add `checkBackupNetworks(ctx, target, b.Ref, v)` (409 `not-restorable`).
   - In `runRestore`, add the same right after `checkRestoreStop` and before step 2 (the stop), as `fail("nothing was changed: " + err.Error())`. It also covers the restore of a safety backup, which uses the same path.
   - Tests in `internal/api`: the fake restorer needs a `BackupConfig` method; see `configVolume` in `plan/restore_precheck_test.go`.
2. **D-427: the follow-up survives a stop:**
   - (a) In `runRestore`, right after `RestoreGuest` returns nil, store a durable fact before anything else: set the `guest:config` step's output to `restored <text>` (still `running`). `e.restored` (`sync.Map`, memory) is lost on a stop.
   - (b) Move the end of the `guest:restore-failed` Issue (today `restore_guest.go` l. 645, `restoreIssue(… StatusUnchanged …)`) into `afterRestore`, after the re-enroll Plan is made (D-427: "the Issue ends only after the follow-up").
   - (c) A new start rule, for example `(e *Engine) recoverRestores(ctx)` in `plan/restore_failed.go`, called from `Recover` (`plan/run.go` l. 1076; one wiring line). For each Target with an open `guest:restore-failed` whose change `from` starts with `running:`, and no running Run:
     - the last restore Run's `guest:config` step output starts with `restored ` → make the re-enroll Plan (as `afterRestore` does; for an adopted Target, the install-command text), then end the Issue with a comment;
     - else update the Issue to `failed: the controller stopped during the restore; the guest may have no disk` (`writeRestoreIssue`, with the way back).
   - Tests: the Issue is open at the revoke and during the restore call (F1 of #359); a stop after the restore call gives exactly one re-enroll Plan; a stop inside the PVE task gives "failed". Check how `Recover` changes a running step (its output must survive).
3. **A refused re-enroll is visible** (host D10: an LXC with a hand-made `mpN` makes `Propose` refuse with D-250). In `afterRestore`, when `e.Propose` returns an error, today only `e.log.Error` happens. Instead, update the open `guest:restore-failed` Issue (see 2b) to `restored, but the re-enroll Plan was refused: <Plumb's reason>` with the way out: remove the addition in Proxmox (`pct set <id> --delete mpN`), then `POST /targets/<t>/reenroll`.
   - **Trap:** while that Issue is open, the T-234 gate refuses every Plan of the Target with 409 `restore-failed`, the re-enroll included. Let `reenroll` pass the gate in this state, or end the Issue with that text as a comment and open no gate. Decide with pl-reviewer.
4. **One re-enroll Plan** (host D12: two restores in a row gave two; approving one made the other stale). In `afterRestore`, before `Propose`, look for a pending or approved re-enroll Plan of the Target (origin `reenroll`). When there is one, make none and name it in the Run output. A restore commits no revision, so the old Plan stays valid.
5. **D-428 gaps** (`internal/issue/issue.go`):
   - (a) l. 218, the `gone` resolve in the report loop: when `systemReason(it)` is empty, still add a comment, for example `Resolved: the guest matches the desired state again.`;
   - (b) `dropIssue` (l. 247): add the comment `Resolved: a Rule covers all of its drift now (D-092).`;
   - (c) in `restore_guest.go`, the two `restoreIssue(ctx, tp.Target, GuestRestoreFailedID, wire.StatusUnchanged, nil)` calls (l. 599, 645) become `endRestoreIssue` with the reasons `the restore did not start; the guest's disks are as before` and `a restore of backup <id> worked in run <n>`;
   - (d) a sweep test in `internal/api`: after the restore, drift and rule tests, every Issue with `resolved_by = system` has a comment. Host case R4: an Issue ended `gone` with no word.
6. **D-429 wiring:** in `runRestore`, when `safety == nil` (the last restore had failed) and the restored volume is a `pre-restore` one (its marker trigger), call `DropSafetyBackups(ctx, target, v.Volid)`. Only older ones go (part 1).

**Traps:**
- The container restore order is not proven (T-247 adds it). Until then a container SDN refusal stays "may have no disk" (`before != nil` guard in `proxmox/restore_guest.go`).
- `endRestoreIssue` comments only for the resources in `issue.systemReason`: `guest:missing`, `agent:silent`, `guest:restore-failed`. `guest:config` has its own comment path in `restore_failed.go`.
- Test helpers: `newSafetyEnv`, `fakeRestorer.fail`, `restoredEnv`.
- CI: `lint, test, build` and the race job (the restore path is critical; pl-reviewer).

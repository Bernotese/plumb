# T-247 – Slice 8 acceptance on the real host

- **Owner:** pl-backend (the one agent on the host)   · **Reviewer:** pl-reviewer (reads the log)   · **QA:** T-245
- **Size:** M   · **Depends on:** all cards merged

## Goal
Slice 8, child card **C11** of T-236. Prove slice 8 on `pve-dev`.

## Context (read only these)
- Plan: `docs/tasks/T-236-slice-8-plan.md` section "Plan" (rows, shared-file order, rules), decisions D-421 … D-435 in `docs/DECISIONS.md`, `docs/plans/ROADMAP.md`. Read row C11, the contract parts and decisions it names, and the review comments of PR #359 on it.
- `AGENTS.md`: "Autonomy and release" (D-326), "Flow" step 5 (D-394), "Git" (remove temporary worktrees), "Test host".

## Acceptance criteria
- [ ] Everything row C11 lists; touch only the files that row gives you (test files next to them, single wiring lines, your own planned guards and marks: no question needed, list them in the PR). Another file: ask `pl-lead` first.
- [ ] The "Real-host acceptance" steps of the plan (incl. step 7: a controller stop during the PVE restore task) and nothing else. Installer `--dry-run` first, result to `pl-lead`, wait for the go; backup first; row counts for new migrations. A restore/backup path is proven with a damaged input. A token step for each human-only rule.
- [ ] No change of rights, network (only what T-244 lists and reverts), `local`, `local-lvm`. Cleanup of this run's objects with the go of `pl-lead`; test secret files by D-416. Log `docs/tasks/T-247.log.md` without addresses, MACs, token ids, keys.
- [ ] Rules: a new command type goes through the real link in a test; every per-Target table says what happens at delete, unbind and VMID reuse; a new operation updates `roleHints.ts`, the tier test and the contract marks; a new attribute gets the guest-value classification test. Name in the report the CI job that proves the card and which of your tests skip locally.
- [ ] No new token right (D-423). Decisions only with numbers from `pl-lead`. Migrations: the next free number at merge time.
- [ ] `make lint test`; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. Any release step. The user's own installation.

- [ ] Restore with a bridge/tag that is no longer named, for an **LXC** (T-241 #369 H1): at which point does PVE refuse – before or after it removes the rootfs? Record the task's exit status text and the state of the volumes. Until this is proven, Plumb says 'not started' only for a VM.
- [ ] T-248: the token can read its own vzdump task log and status; the exact user field of a UPID made with the token.

- [ ] guest:volume-left (T-245 C2, F2): make a left volume on purpose; which way out really ends the Issue – `pvesm free`, or does the config keep an `unusedN` line that needs `qm/pct set --delete unusedN`? The text of the Issue must name the way that works (fix card if not).

## Result (owner, max 8 lines)
branch · commit · tests · notes

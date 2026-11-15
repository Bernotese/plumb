# T-241 – Restore follow-ups (D-427 … D-429)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer (critical)   · **QA:** T-245
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
- [ ] Rules: a new command type goes through the real link in a test; every per-Target table says what happens at delete, unbind and VMID reuse; a new operation updates `roleHints.ts`, the tier test and the contract marks; a new attribute gets the guest-value classification test. Name in the report the CI job that proves the card and which of your tests skip locally.
- [ ] No new token right (D-423). Decisions only with numbers from `pl-lead`. Migrations: the next free number at merge time.
- [ ] `make lint test`; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. Any release step. The user's own installation.

## Result (owner, max 8 lines)
branch · commit · tests · notes

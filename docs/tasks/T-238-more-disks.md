# T-238 – More disks per guest

- **Owner:** pl-backend   · **Reviewer:** pl-architect   · **QA:** T-245
- **Size:** L   · **Depends on:** T-237 merged; T-244 part (1)

## Goal
Slice 8, child card **C2** of T-236. A Target can have data disks: VM `scsiN`, LXC `mpN`, on the install's disk storage; grow; remove as an explicit, destructive step.

## Context (read only these)
- Plan: `docs/tasks/T-236-slice-8-plan.md` section "Plan" (rows, shared-file order, rules), decisions D-421 … D-435 in `docs/DECISIONS.md`, `docs/plans/ROADMAP.md`. Read row C2, the contract parts and decisions it names, and the review comments of PR #359 on it.
- `AGENTS.md`: "Autonomy and release" (D-326), "Flow" step 5 (D-394), "Git" (remove temporary worktrees), "Test host".

## Acceptance criteria
- [ ] Everything row C2 lists; touch only the files that row gives you (test files next to them, single wiring lines, your own planned guards and marks: no question needed, list them in the PR). Another file: ask `pl-lead` first.
- [ ] The facts of T-244 (1) decide the remove path (`unusedN` + free) and what a restore of an older backup does; the Plan says in plain words what data a remove or such a restore loses; a data disk gets a stable serial; say who formats it (D-423).
- [ ] A remove is destructive + human-only by the existing risk rules; the 85 % gate counts every disk.
- [ ] Rules: a new command type goes through the real link in a test; every per-Target table says what happens at delete, unbind and VMID reuse; a new operation updates `roleHints.ts`, the tier test and the contract marks; a new attribute gets the guest-value classification test. Name in the report the CI job that proves the card and which of your tests skip locally.
- [ ] No new token right (D-423). Decisions only with numbers from `pl-lead`. Migrations: the next free number at merge time.
- [ ] `make lint test`; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. Any release step. The user's own installation.

## Result (owner, max 8 lines)
branch · commit · tests · notes

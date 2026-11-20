# T-243 – Tests: log scan for secrets

- **Owner:** pl-reviewer (implementer; Codex is at its weekly limit – if `codex-usage --route` says codex: pl-codex-qa)   · **Reviewer:** pl-architect   · **QA:** T-245
- **Size:** S   · **Depends on:** –

## Goal
Slice 8, child card **C7** of T-236. A test scans the controller's and the agent's logs for every secret kind (D-431).

## Context (read only these)
- Plan: `docs/tasks/T-236-slice-8-plan.md` section "Plan" (rows, shared-file order, rules), decisions D-421 … D-435 in `docs/DECISIONS.md`, `docs/plans/ROADMAP.md`. Read row C7, the contract parts and decisions it names, and the review comments of PR #359 on it.
- `AGENTS.md`: "Autonomy and release" (D-326), "Flow" step 5 (D-394), "Git" (remove temporary worktrees), "Test host".

## Acceptance criteria
- [ ] Everything row C7 lists; touch only the files that row gives you (test files next to them, single wiring lines, your own planned guards and marks: no question needed, list them in the PR). Another file: ask `pl-lead` first.
- [ ] The list of secret kinds is complete (D-431 as amended) and read from one place, so a new kind fails the test until it is added; short needles follow T-213's rule (no chance hits).
- [ ] Rules: a new command type goes through the real link in a test; every per-Target table says what happens at delete, unbind and VMID reuse; a new operation updates `roleHints.ts`, the tier test and the contract marks; a new attribute gets the guest-value classification test. Name in the report the CI job that proves the card and which of your tests skip locally.
- [ ] No new token right (D-423). Decisions only with numbers from `pl-lead`. Migrations: the next free number at merge time.
- [ ] `make lint test`; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. Any release step. The user's own installation.

## Result (owner, max 8 lines)
branch · commit · tests · notes

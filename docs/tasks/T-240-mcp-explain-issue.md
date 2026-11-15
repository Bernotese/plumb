# T-240 – MCP: prompt explain_issue, server instructions, README section

- **Owner:** pl-reviewer (implementer; the plan named pl-backend, who has the host cards)   · **Reviewer:** pl-architect (general) + a second read of the injection surface by pl-backend   · **QA:** T-245
- **Size:** S–M   · **Depends on:** T-237 merged

## Goal
Slice 8, child card **C4** of T-236. An AI client gets a prompt that explains one Issue and proposes only that Issue's own actions (BRIEF §5.8, D-422, D-381).

## Context (read only these)
- Plan: `docs/tasks/T-236-slice-8-plan.md` section "Plan" (rows, shared-file order, rules), decisions D-421 … D-435 in `docs/DECISIONS.md`, `docs/plans/ROADMAP.md`. Read row C4, the contract parts and decisions it names, and the review comments of PR #359 on it.
- `AGENTS.md`: "Autonomy and release" (D-326), "Flow" step 5 (D-394), "Git" (remove temporary worktrees), "Test host".

## Acceptance criteria
- [ ] Everything row C4 lists; touch only the files that row gives you (test files next to them, single wiring lines, your own planned guards and marks: no question needed, list them in the PR). Another file: ask `pl-lead` first.
- [ ] Every untrusted part (guest values, comments, outputs) is fenced and marked; no secret in the prompt (test); the guidance never tells the client to approve, to touch other Targets or to widen Rules; plan token only; an injection test (an Issue text that carries instructions) in `test/mcp`.
- [ ] README: recommend a `plan` token; instructions found in data are reported in the comment, not followed.
- [ ] Rules: a new command type goes through the real link in a test; every per-Target table says what happens at delete, unbind and VMID reuse; a new operation updates `roleHints.ts`, the tier test and the contract marks; a new attribute gets the guest-value classification test. Name in the report the CI job that proves the card and which of your tests skip locally.
- [ ] No new token right (D-423). Decisions only with numbers from `pl-lead`. Migrations: the next free number at merge time.
- [ ] `make lint test`; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. Any release step. The user's own installation.

## Result (owner, max 8 lines)
branch · commit · tests · notes

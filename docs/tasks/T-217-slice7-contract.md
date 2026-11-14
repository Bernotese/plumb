# T-217 – Slice 7 contract (more NICs + VLAN, guest backups, restore over)

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer (rights, backup secrets = critical)   · **QA:** –
- **Size:** M   · **Depends on:** –

## Goal
Slice 7, child card **C1** of T-215: the contracts so that C2 … C7 can start.

## Context (read only these)
- Plan: `docs/tasks/T-215-slice-7-plan.md` section "Plan" (until PR #321 is merged: `git show origin/pl-architect/T-215-slice-7-plan:docs/tasks/T-215-slice-7-plan.md`), decisions `docs/decisions/T-215.md` on that branch (D-398 … D-415), ROADMAP `docs/plans/ROADMAP.md` on that branch. Row C1, the contract text, the shared-file order, the rules of D-413.

## Acceptance criteria
- [ ] Everything row C1 lists. No "restore after the guest is gone" (D-411). The rights named in the contract are exactly D-379 / D-380.
- [ ] Every new operation: `x-plumb-planned` mark, `x-plumb-role`, `x-plumb-rate`, `web/src/lib/roleHints.ts`, and for a new human-only reason the text in `web/src/lib/view.ts` (the contract-enum tests fail else).
- [ ] Every per-Target table of slice 7 says what happens at delete, unbind and VMID reuse (D-397).
- [ ] `make lint test`; PR to `main`; report.

## Out of scope
- Code of other rows.

## Result (owner, max 8 lines)
branch · commit · tests · notes

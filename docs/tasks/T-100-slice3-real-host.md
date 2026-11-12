# T-100 – Slice 3 real-host acceptance

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** T-091 … T-097   · **Parent:** T-089 (C10)

## Goal
Prove slice 3 on the real test host.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-100.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [ ] Allowed host actions: bring the controller to main; make test guests in the pool `plumb` (VMIDs 9000–9099) and remove them at the end; one Image build. Nothing else without asking pl-lead
- [ ] The one-time manual step on a pre-T-091 VM and LXC
- [ ] Update a T-091+ agent with a lower version (VM and LXC), incl. one forced auto-revert
- [ ] New resources and `docker-compose` in a VM and an LXC; if the LXC needs `keyctl`: stop, record it, `docker-compose` stays VM-only (Question 4)
- [ ] T-084 leftover: the thin-pool metadata note shows in a real Image build Plan
- [ ] Log in `docs/tasks/T-100.log.md` without addresses, token ids, keys; host clean at the end

## Out of scope
- New token rights; anything in AGENTS.md "Test host" that is forbidden

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

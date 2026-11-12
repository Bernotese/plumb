# T-091 – Agent self-update with auto-revert

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** T-090   · **Parent:** T-089 (C2)

## Goal
A Plumb agent can update itself on command and comes back on the old binary when the new one fails.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-091.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [ ] Command `update`: fetch `wire.PathBinary` from the pinned controller, check SHA-256, self-test of the new binary, atomic swap, keep `.prev`, update marker, exit for a systemd restart (D-258)
- [ ] In the guest: crash loop or no successful `hello` within 5 min restores `.prev` and restarts
- [ ] `hello` sends the capability list (D-259)
- [ ] CI guest test (Debian 13, Ubuntu 26.04, VM and LXC image): good update; a binary that passes the self-test but cannot connect comes back as `.prev`
- [ ] Doc of the one-time manual step for agents from before this card (D-258, Question 6): `pct exec` for an LXC, SSH or console for a VM
- [ ] `make lint test build` and `make test-guest` pass

## Out of scope
- Controller side (T-092)
- No URL field in the command; no new token right

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

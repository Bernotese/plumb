# T-119 – Starter Block `firewall`

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-116   · **Parent:** T-107 (C9)

## Goal
A starter Block that opens SSH and listed ports and closes the rest of input.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C9, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-119.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] Package `nftables` + resource `firewall`; parameters: ports, SSH port; render tests
- [ ] Works on VM and LXC Targets (CI guest test or the T-116 test extended)

## Out of scope
- Output rules, forwarding
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

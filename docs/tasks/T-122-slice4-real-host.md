# T-122 – Slice 4 real-host acceptance

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** T-112 … T-119   · **Parent:** T-107 (C12)

## Goal
Prove slice 4 on the real test host.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C12, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-122.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] Allowed host actions: pl-lead lists them in this card before the start (incl. the backup storage for the test host). Dry-run first, then wait for the go
- [ ] Backup → off-host copy → `--remove --keep-guests` with a VM and an LXC in the pool (a wrong recovery key stops it before any change; a kept VM starts between remove and restore) → `--restore` (a wrong pool is refused) → restored mode, confirm, agents come back, Plan clean, revoked token stays off, `guest:missing` and unknown guests shown
- [ ] power / tags / grow on VM and LXC; a `firewall` change that cuts the link rolls back by itself; a `drop` without SSH is refused
- [ ] Recovery key handling per AGENTS.md (D-180); log `docs/tasks/T-122.log.md` without addresses, token ids, keys; host clean at the end

## Out of scope
- New token rights; anything AGENTS.md "Test host" forbids
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

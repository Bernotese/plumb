# T-111 – Slice 4 contract: day 2, safe apply, firewall, raw mode

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** –   · **Parent:** T-107 (C1b)

## Goal
Fix the contract for `power`, `tags`, disk grow, the agent command `grow`, safe apply, `firewall` and raw mode.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C1b, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-111.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] `guest.power`, `guest.tags`, grow-only `guest.disk` in the Target schema with risk classes (D-291)
- [ ] Agent command `grow`; safe-apply wire (`confirm` window 90 s, result `rolled-back`, D-293); capability entries (D-259 gate)
- [ ] Schema of `firewall` incl. the SSH rule and `allow_no_ssh` (D-295); raw-mode marks on Issues and the override editor API (D-296)
- [ ] OpenAPI, schemas and `docs/ARCHITECTURE.md` updated; `make lint test build` green

## Out of scope
- Backup and restore (T-110)
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

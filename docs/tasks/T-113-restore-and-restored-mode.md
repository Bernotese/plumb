# T-113 – Restore and restored mode

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** T-112   · **Parent:** T-107 (C3)

## Goal
A restored Plumb knows its guests again and changes nothing until a person confirms that the old controller is off.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C3, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-113.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] `plumb restore <file>` with the recovery key; drops all sessions, disables every API token, forces a new admin password (D-290)
- [ ] Restored mode stops every writer: Proxmox writes, agent commands except observe, Plan approval, image schedules, retention, agent updates, Revert, backup schedule; API 409 `restored-mode`
- [ ] After the confirm: `guest:missing`, `agent:silent` (no hello in 10 min), unknown guests listed and never touched
- [ ] Tests for each stopped writer and for the token/session rules; `make lint test build` green

- [ ] Migration numbers: 0013 is T-116 (PR #213); T-112 takes 0014, T-113 takes 0015. The loader refuses gaps: merge main first

## Out of scope
- Installer flags (T-114); adoption of guests
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

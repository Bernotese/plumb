# T-115 – Proxmox day 2: power, tags, disk grow

- **Owner:** pl-reviewer   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** T-111 (T-117 for the VM grow end to end)   · **Parent:** T-107 (C5)

## Goal
A Target can be stopped and started, tagged and its disk grown, by Plan.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C5, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-115.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] `power` as desired state: start `reversible`, stop `disruptive` with `allow_restart`; agent steps on a stopped Target are `skipped`; an offline agent of a stopped Target is no Issue (D-291)
- [ ] `tags` with drift; a refusal by `user-tag-access` gives a clear text
- [ ] Disk grow only (smaller refused at save), disk gate D-219, VM resize + agent `grow`, LXC resize
- [ ] Driver + engine tests on the fake PVE; `make lint test build` green; no new token right

## Out of scope
- Disk shrink, more NICs, change of the main ip
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-112 – Backup in the controller

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-110   · **Parent:** T-107 (C2)

## Goal
The controller writes a scheduled, encrypted backup of its whole state.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C2, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-112.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] Online SQLite snapshot + link dir in one file, chunked AEAD under a key derived from the master key (D-288); temp + rename; pointer `latest`
- [ ] Schedule (daily) and retention (keep 7); API and events of T-110; a failed backup is visible (Issue or event per the contract)
- [ ] No secret, key or plain data in logs; the Proxmox token secret and the master key are not in the file
- [ ] Tests incl. a wrong key, a cut file, a changed chunk; `make lint test build` green

- [ ] Migration numbers: 0013 is T-116 (PR #213); T-112 takes 0014, T-113 takes 0015. The loader refuses gaps: merge main first

## Out of scope
- Restore (T-113), installer (T-114)
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

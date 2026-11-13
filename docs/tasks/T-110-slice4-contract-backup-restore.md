# T-110 – Slice 4 contract: backup and restore

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** –   · **Parent:** T-107 (C1a)

## Goal
Fix the contract for backup, off-host copy, replace and restore before any code.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C1a, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-110.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] Backup file format (header with fingerprint, chunked AEAD, D-288); API `GET /backups`, `POST /backups`, state of the off-host copy; no download
- [ ] `plumb restore`; installer flags `--backup-storage`, `--remove --keep-guests`, `--restore` with their pre-checks (D-289, D-290 incl. N1–N7)
- [ ] Restored mode: API 409 `restored-mode`, the full list of what is stopped, confirm step, token re-enable; Issues `guest:missing`, `agent:silent`
- [ ] OpenAPI, wire/state schemas and `docs/ARCHITECTURE.md` updated; `make lint test build` green (contract tests, no feature code)

## Out of scope
- Day 2 and firewall (T-111)
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-118 – Web: backups, restored mode, day 2 fields, raw mode

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** T-110, T-111 (mock first); T-112, T-113, T-115 for the real API   · **Parent:** T-107 (C8)

## Goal
The UI shows and controls everything slice 4 adds.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C8, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-118.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] Settings → Backups: list, state, off-host copy state, "back up now"
- [ ] Restored mode: banner on every screen, confirm dialog, token re-enable, forced new password
- [ ] Target form: `power`, `tags`, disk grow (grow only); Block picker filtered by target kind
- [ ] Raw mode: Issue action "Accept as raw for this Target"; Target page lists raw resources, edit, "back to the Block" (D-296)
- [ ] Component + e2e tests; `browser-shot` proof light and dark; `make lint test build` green

## Out of scope
- Backup download
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

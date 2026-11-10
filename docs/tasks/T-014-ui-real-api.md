# T-014 – UI on the real API

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-006, T-011   · **Parallel with:** –
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
The UI works against the real controller.

## Context (read only these)
- `docs/ARCHITECTURE.md` §4
- `web/` (from T-006)
- `bin/plumb serve --dev` (from T-011)

## Acceptance criteria
- [ ] Every screen of T-006 uses `/api/v1` of `plumb serve --dev`. The mock stays only in component tests.
- [ ] Login, logout and an expired session work. The UI never stores a password or token in `localStorage`.
- [ ] Live progress and the issue inbox update from the event stream without reload. A lost stream reconnects.
- [ ] API errors show the problem title and detail.
- [ ] The PR has `browser-shot` screenshots: create target → plan → run → issue → revert.
- [ ] Tests: `npm test` passes. One Playwright test covers the same path.

## Out of scope
- New screens.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

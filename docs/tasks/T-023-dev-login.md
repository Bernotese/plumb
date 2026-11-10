# T-023 – Dev login and demo data for screenshots (server part of D-080)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S–M   · **Depends on:** T-011 (merged)   · **Parallel with:** T-014 (only its screenshot criterion waits)

## Goal
`browser-shot` can take screenshots of authenticated screens on the real API.

## Context (read only these)
- `docs/DECISIONS.md` D-080 (the full rule) and D-070 … D-074.
- `docs/tasks/T-014-ui-real-api.md` (criteria added by pl-architect).
- `cmd/plumb` serve, `internal/api` session code.

## Acceptance criteria
- [ ] `plumb serve --dev --dev-demo` seeds the demo data of D-080 and prints the login URL once. The token is 256 random bits, new at every start, memory only, compared in constant time.
- [ ] `--dev-demo` without `--dev` is refused at start. Without the flag the route is not registered: `GET /dev/login` returns 404 (test with a production-mode server).
- [ ] `next` accepts only a local path (no scheme, no `//`, no backslash): open-redirect test.
- [ ] The dev mode marker still refuses a production data dir (existing guard), with a test for `--dev-demo`.
- [ ] Tests: `make lint test build` passes. One `browser-shot` image of an authenticated screen in the PR.

## Out of scope
- UI changes (T-014). A `--cookie` option for `browser-shot` (rejected in D-080).

## Result (owner, max 8 lines)
branch · commit · tests · notes

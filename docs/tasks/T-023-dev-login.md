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
- [x] `plumb serve --dev --dev-demo` seeds the demo data of D-080 and prints the login URL once. The token is 256 random bits, new at every start, memory only, compared in constant time.
- [x] `--dev-demo` without `--dev` is refused at start. Without the flag the route is not registered: `GET /dev/login` returns 404 (test with a production-mode server).
- [x] `next` accepts only a local path (no scheme, no `//`, no backslash): open-redirect test.
- [x] The dev mode marker still refuses a production data dir (existing guard), with a test for `--dev-demo`.
- [x] Tests: `make lint test build` passes. One `browser-shot` image of an authenticated screen in the PR.

## Out of scope
- UI changes (T-014). A `--cookie` option for `browser-shot` (rejected in D-080).

## Result (owner, max 8 lines)
`pl-backend/T-023-dev-login` · tests: `make lint test build` → pass.
- `serve --dev --dev-demo`: user `dev` (password hash `!`, no login form accepts it), open drift Issue on web2, pending Plan for web1; prints `dev login: http://<addr>/dev/login?token=…&next=/` once. Token: 256 bits, new per start, memory only.
- `api`: `GET /dev/login` registered only with `Dev` and a token; constant-time compare; wrong/missing token → 404; normal session (D-070 cookie, lifetime); 303 to a local `next` (else `/`).
- Tests: route absent on a production server and on dev without token; wrong tokens; 10 open-redirect cases; `--dev-demo` without `--dev`; `--dev-demo` on a production data dir.
- Screenshot: `docs/screenshots/T-023-dev-login.png` (browser-shot via the dev login link, targets list).

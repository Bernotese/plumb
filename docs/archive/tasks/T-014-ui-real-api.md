# T-014 – UI on the real API

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-006, T-011   · **Parallel with:** T-015
- **Status:** review

## Goal
The UI works against the real controller.

## Context (read only these)
- `docs/ARCHITECTURE.md` §4
- `web/` (from T-006)
- `bin/plumb serve --dev` (from T-011)

## Acceptance criteria
- [ ] Every screen of T-006 uses `/api/v1` of `plumb serve --dev`. The mock stays only in component tests.
- [ ] Login, forced password change, logout and an expired session work. The UI never stores a password or token in `localStorage`.
- [ ] Live progress and the issue inbox update from the event stream without reload. A lost stream reconnects.
- [ ] API errors show the problem title and detail.
- [ ] The PR has `browser-shot` screenshots: create target → plan → run → issue → revert.
- [ ] Tests: `npm test` passes. One Playwright test covers the same path.
- [ ] **Dev login, server part (pl-backend, D-080):** `plumb serve --dev --dev-demo` seeds, on an empty store, demo data on top of the dev seed: one pending Plan, one open drift Issue and the user `dev` (no usable password, no forced change). It prints one line: `dev login: /dev/login?token=<token>&next=/`. The token is 256 random bits, new at every start, in memory only. The request log never holds the token.
- [ ] **Dev login, route (pl-backend):** `GET /dev/login?token=…&next=…` compares the token in constant time, makes a normal session for `dev` (cookie and lifetime of D-070) and answers 303 to `next`. `next` must be a local path (one leading `/`, no `//`, no `\`); else the route uses `/`. A wrong or missing token is 404.
- [ ] **Dev login, guard (pl-backend):** `--dev-demo` without `--dev` stops the start with an error. The `api` package registers the route only when `Dev` is true and a token is set. Without the flag the path is 404 and the user `dev` does not exist. Tests cover these three cases.
- [ ] **Dev login, UI part (pl-frontend):** the screenshots run against `serve --dev --dev-demo --listen 0.0.0.0:<port>` with `browser-shot "http://10.0.0.50:<port>/dev/login?token=…&next=<screen>"`. The UI has no link to `/dev/login` and no code for it. The Playwright test uses the real login form.

## Out of scope
- New screens.

## Plan (architect, optional)
- Screenshots with a session (D-080): `browser-shot` cannot type, so the server gets a dev login. The option `--cookie` for `browser-shot` is rejected: the tool is outside the repo, so the flow would have no test and no review.
- The server part (the three pl-backend criteria) is a small change in `cmd/plumb/serve.go`, `cmd/plumb/dev.go` and `internal/api`. pl-backend does it as its own card and PR; pl-lead creates the card. pl-reviewer checks the guard.
- The UI work does not wait for the server part. Only the screenshot criterion needs it.
- The open drift Issue needs a simulated agent that reports a drift (`link/linktest`). The pending Plan is a normal proposal that the seed does not approve.

## Result (owner, max 8 lines)
- Branch `pl-frontend/T-014-ui-real-api`, PR #28. `web/src/lib/http.ts`: `PlumbApi` over `/api/v1` (paging via `next`, problem+json → `ApiError`, failed fetch → `NetworkError`, 401 outside login → "session ended" and back to login). Every build uses it; the mock is only in `npm run dev:mock` (development) and the unit tests (prod `dist` is checked for mock code).
- Event stream: named SSE events, dedupe by `seq`, own reconnect from the last `seq` with backoff when the browser gives up; run view, issue inbox and target list update live.
- No password or token in browser storage (`storage.test.ts`: only `theme.ts` uses storage). Problem title and detail on every error.
- Tests: `npm test` 81 pass; `make test-e2e` (Playwright, real login form, create target → plan → run → issue → revert → resolved → logout) passes locally and in CI; `make lint test build` pass.
- Screenshots via the T-023 dev login (`serve --dev --dev-demo`), light and dark, on branch `pl-frontend/T-014-screens`.
- Found: in `--dev`, a newly created target gets no simulated agent, so its run stays `running` (steps after `guest:config` pending). Server-side (seedDev starts agents only for web1/web2).

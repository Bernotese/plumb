# T-011 – API, auth and events

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-010, T-017   · **Parallel with:** T-006
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
Everything the slice can do is reachable through the documented API, with login, token tiers and a live event stream.

## Context (read only these)
- `docs/ARCHITECTURE.md` §4, §7, §9
- `api/openapi.yaml`
- `internal/plan`, `internal/issue`, `internal/event`

## Acceptance criteria
- [x] Handlers for every slice endpoint in `api/openapi.yaml`, including issue comments (tier `plan` or higher). Errors are problem+json. The words and ids are those of D-045; there is no mapping layer.
- [x] First start creates the admin user with a random password and prints it once. The first login must change it. Passwords use argon2id.
- [x] Session cookie is `HttpOnly`, `Secure`, `SameSite=Strict`. State-changing calls check the `Origin`. Login has a rate limit.
- [x] API tokens: tiers `read`, `plan`, `apply` (+ `max_risk`). `admin` is the tier of a human session only (D-046). Tokens are stored as hashes.
- [x] Negative tests: a `plan` token cannot approve; an `apply` token cannot approve above its `max_risk`; no token can approve a `destructive` Plan; a token with `max_risk: destructive` cannot be created; no endpoint returns a secret or a token hash.
- [x] `GET /events` is SSE and resumes from `Last-Event-ID` or `since`.
- [x] `plumb serve --dev`: fake driver, two simulated targets, seeded data, plain HTTP, cookie without `Secure`. `--dev` refuses to start with the Proxmox driver. Without `--dev`, plain HTTP is refused.
- [x] Contract test: it takes the response schemas from `api/openapi.yaml` and validates real responses with `santhosh-tekuri/jsonschema`. No new dependency.
- [x] Tests: `go test ./internal/api/...` passes: every tier against every endpoint, plus the contract test.
- [x] From T-017 QA: `cmd/plumb` imports package `web` and serves the embedded UI at `/` (T-017 only built the embed package). Test: `GET /` returns the built `index.html`; unknown non-API paths fall back to it.
- [x] From the T-017 contract check: `Event.type` in `api/openapi.yaml` is a closed enum. Add every new event type there first.
- [x] From T-009 QA: `plumb` embeds the real `plumb-agent` binary (built first by `make build`) and wires it into `link.Config.AgentBinary`. Test: `GET /agent/v1/binary` from the built `plumb` returns a binary whose `version` output matches. (T-009 serves only a fixture.)

- [x] From the T-010 review (m5): a pending Plan keeps its Issue `resolving` until the Plan goes stale. Add a way to drop a pending Plan (for example `DELETE /plans/{id}` → stale → the Issue opens again).
- [x] From the T-010 re-review: `cmd/plumb` calls `Engine.Recover` before it starts link, API and `PollGuests`. Follow-up minors (see pl-reviewer's last PR #14 comment): n1 a Run that fails early leaves the old watch list; n2 misleading busy error on a double approve; n3 Recover runs only before serving.

## Out of scope
- Token management UI, secrets, export/import endpoints.

## Plan (architect, optional)

## Result (owner, max 8 lines)
`pl-backend/T-011-api-auth-events` · tests: `make lint test build check-embed` → pass locally; CI adds `-race`.
- api: every slice operation of `api/openapi.yaml` plus `DELETE /plans/{id}` (m5), problem+json (D-074), sessions/tokens/Origin/login limit (D-070), SSE with `since`/`Last-Event-ID` and keep-alive, embedded UI with fallback.
- Tests: tier matrix (every caller × every endpoint), approval rules, token creation rules, secret scan of all bodies, contract test (every response and every event against the spec, plus a self-check), SSE resume, UI fallback.
- serve: `--dev` (D-072), Recover before serving, admin password once; agent binary embedded and checked by `make check-embed` (D-071).
- Event names now fit the closed enum (D-073). T-010 re-review n1–n3 fixed (watch after early failure, clear double-approve error, Recover comment).
- Open: token management has no API yet (out of scope); `api.CreateToken` exists for it and for tests.

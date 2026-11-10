# T-011 – API, auth and events

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-010   · **Parallel with:** –
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
Everything the slice can do is reachable through the documented API, with login, token tiers and a live event stream.

## Context (read only these)
- `docs/ARCHITECTURE.md` §4, §7
- `api/openapi.yaml`
- `internal/plan`, `internal/issue`, `internal/event`

## Acceptance criteria
- [ ] Handlers for every slice endpoint in `api/openapi.yaml`. Errors are problem+json.
- [ ] First start creates the admin user with a random password and prints it once. Passwords use argon2id.
- [ ] Session cookie is `HttpOnly`, `Secure`, `SameSite=Strict`. State-changing calls check the `Origin`. Login has a rate limit.
- [ ] API tokens: tiers `read`, `plan`, `apply` (+ `max_risk`), `admin`. Tokens are stored as hashes. A token never approves a `destructive` Plan.
- [ ] `GET /events` is SSE and resumes from `Last-Event-ID` or `since`.
- [ ] `plumb serve --dev`: fake driver, two simulated targets, seeded data, plain HTTP. `plumb serve` without `--dev` refuses plain HTTP.
- [ ] Tests: `go test ./internal/api/...` passes: every tier against every endpoint, and a contract test that checks responses against `api/openapi.yaml`.

## Out of scope
- Token management UI, secrets, export/import endpoints.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

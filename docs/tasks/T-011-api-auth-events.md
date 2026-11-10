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
- [ ] Handlers for every slice endpoint in `api/openapi.yaml`, including issue comments (tier `plan` or higher). Errors are problem+json. The words and ids are those of D-045; there is no mapping layer.
- [ ] First start creates the admin user with a random password and prints it once. The first login must change it. Passwords use argon2id.
- [ ] Session cookie is `HttpOnly`, `Secure`, `SameSite=Strict`. State-changing calls check the `Origin`. Login has a rate limit.
- [ ] API tokens: tiers `read`, `plan`, `apply` (+ `max_risk`). `admin` is the tier of a human session only (D-046). Tokens are stored as hashes.
- [ ] Negative tests: a `plan` token cannot approve; an `apply` token cannot approve above its `max_risk`; no token can approve a `destructive` Plan; a token with `max_risk: destructive` cannot be created; no endpoint returns a secret or a token hash.
- [ ] `GET /events` is SSE and resumes from `Last-Event-ID` or `since`.
- [ ] `plumb serve --dev`: fake driver, two simulated targets, seeded data, plain HTTP, cookie without `Secure`. `--dev` refuses to start with the Proxmox driver. Without `--dev`, plain HTTP is refused.
- [ ] Contract test: it takes the response schemas from `api/openapi.yaml` and validates real responses with `santhosh-tekuri/jsonschema`. No new dependency.
- [ ] Tests: `go test ./internal/api/...` passes: every tier against every endpoint, plus the contract test.

## Out of scope
- Token management UI, secrets, export/import endpoints.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

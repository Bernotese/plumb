# T-030 – API token management: API

- **Owner:** pl-reviewer (as implementer; review by pl-architect)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-027

## Goal
A human can list, create and revoke API tokens through `/api/v1/tokens`.

## Context (read only these)
- `docs/DECISIONS.md` D-046, D-070, D-085 · `api/openapi.yaml` – `/tokens` operations
- `internal/api/secret.go` (`CreateToken`), `internal/api/handlers.go`, `internal/store/auth.go`

## Files
`internal/store/auth.go` (`Tokens` list) · `internal/api/handlers.go` (+ `tokens.go`) · `cmd/plumb/dev.go` (demo token) · tests

## Acceptance criteria
- [x] The operations of this card lose `x-plumb-planned` in `api/openapi.yaml` and get their rows in the tier matrix of `internal/api` (D-100).
- [x] `GET /tokens`, `POST /tokens`, `DELETE /tokens/{id}` per the contract. Human session only: every token tier gets 403 `human-required`.
- [x] The bearer value is in the `POST` answer only. No list, log, event or error holds it or its hash.
- [x] A revoked token fails at its next call (401), and its open event streams end at once (D-070). Revoke is soft: the row stays for the history of "who approved".
- [x] `POST /tokens` answers with `Cache-Control: no-store`.
- [x] 422 for a tier/`max_risk` pair that D-046 forbids, with a pointer.
- [x] Events `token.created`, `token.revoked` with id, name and tier.
- [x] `--dev-demo` seeds one `plan` token (its bearer is not printed).
- [x] Stays inside approved decisions (D-046). No migration.

## Tests
- API: tier table; create → use → revoke → 401; an open SSE stream of the token closes on revoke; bearer never in `GET /tokens`; contract test covers the operations. `make lint test` passes.

## Out of scope
- Expiry, rotation, per-target scopes. UI (T-035).

## Result (owner, max 8 lines)
- Branch `pl-reviewer/T-030-token-api`. `make lint test` passes.
- `internal/api/tokens.go`: list (newest first, cursor = row order), create (422 with pointers, `no-store`, `Location`), revoke (soft; repeat 204, unknown 404). Routes are session-only and not allowed during a password reset.
- Revoke ends the token's open event streams at once: `events.go` keeps a cancel func per stream (before: only at the next keep-alive).
- Events `token.created` / `token.revoked` hold id, name, tier. A repeated revoke emits no event.
- `CreateToken` counts the name in characters (contract `maxLength`), not bytes.
- Tests: `tokens_test.go` (lifecycle, D-046 validation, paging, stream end), tier matrix rows, `TestContract` calls, demo token test in `cmd/plumb`.

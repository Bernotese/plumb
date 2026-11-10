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
- [ ] The operations of this card lose `x-plumb-planned` in `api/openapi.yaml` and get their rows in the tier matrix of `internal/api` (D-100).
- [ ] `GET /tokens`, `POST /tokens`, `DELETE /tokens/{id}` per the contract. Human session only: every token tier gets 403 `human-required`.
- [ ] The bearer value is in the `POST` answer only. No list, log, event or error holds it or its hash.
- [ ] A revoked token fails at its next call (401), and its open event streams end at once (D-070). Revoke is soft: the row stays for the history of "who approved".
- [ ] `POST /tokens` answers with `Cache-Control: no-store`.
- [ ] 422 for a tier/`max_risk` pair that D-046 forbids, with a pointer.
- [ ] Events `token.created`, `token.revoked` with id, name and tier.
- [ ] `--dev-demo` seeds one `plan` token (its bearer is not printed).
- [ ] Stays inside approved decisions (D-046). No migration.

## Tests
- API: tier table; create → use → revoke → 401; an open SSE stream of the token closes on revoke; bearer never in `GET /tokens`; contract test covers the operations. `make lint test` passes.

## Out of scope
- Expiry, rotation, per-target scopes. UI (T-035).

## Result (owner, max 8 lines)
branch · commit · tests · notes

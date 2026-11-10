# T-028 – Secret store: key file, encryption, API

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-027

## Goal
Plumb stores secrets encrypted under the master key. A human can set, list and delete them through the API. Nothing reads a value back.

## Context (read only these)
- `docs/ARCHITECTURE.md` §7, §12 · `docs/DECISIONS.md` D-030, D-086, D-087, D-096
- `internal/secret/doc.go`, `internal/store/auth.go` (style), `internal/api/handlers.go`, `cmd/plumb/serve.go`
- `api/openapi.yaml` – `/secrets` operations (T-027)

## Files
`internal/secret/*` · `internal/store/secret.go` · `internal/store/migrations/0003_secret.sql` · `internal/api/handlers.go` (+ a `secrets.go`) · `cmd/plumb/serve.go`, `cmd/plumb/key.go` · tests next to the code

## Acceptance criteria
- [ ] Key file per D-086: created at the first start with mode 0600, recovery key printed once, flag `--key-file`. A key file that group or others can read stops the start (as D-064).
- [ ] `plumb key restore` reads the recovery key from stdin, checks the checksum and writes the key file. It refuses to overwrite a file.
- [ ] Key check value in the database. Start with stored secrets and a missing or wrong key: exit with a message that names `plumb key restore`.
- [ ] `secret.Store`: `Put`, `Delete`, `List` (no values), `Resolve(names)` (values; used by `plan` only, T-029). AES-256-GCM, nonce, additional data and limits per D-087. Only `store` imports `database/sql`.
- [ ] API per the contract: tiers, 409 `secret-in-use` is prepared with a `usedBy` hook (T-029 fills it; until then no secret is in use). Events `secret.updated`, `secret.deleted` with the name only.
- [ ] No log line, error text or event holds a value. The request body of `PUT /secrets/{name}` is never logged.
- [ ] `--dev` creates a throwaway key in the dev data directory and prints no recovery key. `--dev-demo` seeds one secret `demo-db-password`.
- [ ] Stays inside approved decisions (D-086, D-087). Stdlib and `golang.org/x/crypto` only.

## Tests
- Unit: round trip; wrong key; changed ciphertext, nonce, name or version fails; swapped rows fail; limits; recovery key round trip and bad checksum; key file modes.
- API: tier table for the three operations; `GET /secrets` body never holds the value; contract test covers the new operations.
- `make lint test` passes.

## Out of scope
- References in documents and delivery to agents (T-029). Export (T-033). Key rotation. The `backup=0` volume (installer, T-013, blocked).

## Result (owner, max 8 lines)
branch · commit · tests · notes

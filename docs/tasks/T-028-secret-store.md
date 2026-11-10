# T-028 – Secret store: key file, encryption, API

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-027

## Goal
Plumb stores secrets encrypted under the master key. A human can set, list and delete them through the API. Nothing reads a value back. The key and its recovery key never reach a log or a backup.

## Context (read only these)
- `docs/ARCHITECTURE.md` §7, §12 · `docs/DECISIONS.md` D-030, D-086, D-087, D-096
- `internal/secret/doc.go`, `internal/store/auth.go` (style), `internal/api/handlers.go`, `cmd/plumb/serve.go`
- `api/openapi.yaml` – `/secrets` operations (T-027)

## Files
`internal/secret/*` · `internal/store/secret.go` · `internal/store/migrations/0003_secret.sql` · `internal/api/handlers.go` (+ a `secrets.go`) · `cmd/plumb/serve.go`, `cmd/plumb/key.go` · tests next to the code

## Acceptance criteria
- [x] The operations of this card lose `x-plumb-planned` in `api/openapi.yaml` and get their rows in the tier matrix of `internal/api` (D-100).
- [x] `plumb key init` per D-086 is the only code that makes a production key: mode 0600, flag `--key-file`. It shows the recovery key once on the terminal. It writes it to no log and no file. It refuses when stdout is not a terminal, when a key file exists, and when the key directory is on the same file system as the database.
- [x] `plumb serve` without `--dev` never makes a key. A missing key file **always** stops the start, also with zero stored secrets; the message names `plumb key init` and `plumb key restore`. A key file that group or others can read stops the start (as D-064).
- [x] `plumb key restore` reads the recovery key from the terminal (no echo) or stdin, checks the checksum and writes the key file. It refuses to overwrite a file.
- [x] Key check value in the database. A wrong key stops the start.
- [x] `secret.Store`: `Put`, `Delete`, `List` (no values), `Resolve(names)` (values; used by `plan` only, T-029). AES-256-GCM, nonce and limits per D-087. The additional data is purpose, name and version, each with a length prefix. `version` comes from one store-wide counter that never goes back. Only `store` imports `database/sql`.
- [x] API per the contract: tiers, 409 `secret-in-use` is prepared with a `usedBy` hook for the head revision and pending Plans (T-029 fills it; until then no secret is in use). Events `secret.updated`, `secret.deleted` with the name only.
- [x] No log line, error text or event holds a value. The request body of `PUT /secrets/{name}` is never logged.
- [x] `--dev` creates a throwaway key in the dev data directory and prints no recovery key. `--dev-demo` seeds one secret `demo-db-password`.
- [x] Stays inside approved decisions (D-086, D-087). Stdlib and `golang.org/x/crypto` only.

## Tests
- Unit: round trip; wrong key; changed ciphertext, nonce, name or version fails; swapped rows fail; a row of a deleted secret put back under a new secret of the same name fails; two (name, version) pairs never give the same additional data; limits; recovery key round trip and bad checksum; key file modes.
- Start: `serve` without a key file exits non-zero on an empty database and creates no file; the captured output of `serve` never holds the recovery key; `key init` without a terminal, with an existing file, and on the database file system is refused.
- API: tier table for the three operations; `GET /secrets` body never holds the value; contract test covers the new operations.
- `make lint test` passes.

## Out of scope
- References in documents and delivery to agents (T-029). Export (T-033). Key rotation. The `backup=0` volume and the call of `plumb key init` in the installer (T-013, blocked; `pl-lead` adds it to that card).

## Result (owner, max 8 lines)
- Branch `pl-backend/T-028-secret-store`. Tests: `make lint test` → pass.
- `internal/secret`: key file, recovery key (D-103), HKDF purposes, key check (D-105), AES-GCM store (D-106). `store/secret.go` + migration 0003.
- API `GET/PUT/DELETE /secrets` (D-107); usage hook for T-029 via `secret.Store.SetUsage` (D-108).
- `plumb key init|restore` (`cmd/plumb/key.go`, ioctl terminal check, Linux only); `serve` loads the key before it creates any file.
- Note for T-029: delete-vs-new-Plan is not atomic; the Run must fail on a missing secret.
- Note for T-013: the installer needs the key volume mounted at `<data>/key` before `plumb key init`.

# T-033 – Export and import of the state

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-029, T-031, T-032

## Goal
A user can export the desired state as one readable YAML file and import it into this or a fresh Plumb through a Plan.

## Context (read only these)
- `docs/DECISIONS.md` D-010, D-030, D-070, D-094, D-095, D-099 · `docs/ARCHITECTURE.md` §12
- `internal/state/state.go`, `commit.go` · `internal/secret/*` · `internal/plan/plan.go` · `api/openapi.yaml` – `/export`, `/import`

## Files
`internal/state/export.go` (encode, decode, limits) · `internal/secret/bundle.go` (passphrase bundle) · `internal/api/*` · `internal/plan/*` (origin `import`) · tests, `internal/state/testdata/export/*`

## Acceptance criteria
- [x] The operations of this card lose `x-plumb-planned` in `api/openapi.yaml` and get their rows in the tier matrix of `internal/api` (D-100).
- [x] Export format of D-094. The output is stable: the same revision gives the same bytes, except the time in the header.
- [x] `GET /export` (tier `read`) holds no secret value. `POST /export` (human session, passphrase ≥ 12 characters) adds the `SecretBundle`. Both answers send `Cache-Control: no-store`.
- [x] The bundle names the fixed argon2id parameters of format 1 (D-094). An import refuses a bundle with any other parameter before it runs argon2id, and runs it in the argon2 slots of D-070 (429 when they are busy).
- [x] `POST /import`: modes `replace` and `merge`, Plan with origin `import`, limits of D-095 (size, anchors, aliases, unknown `format`), 422 with pointers for an invalid document.
- [x] Secrets of the bundle (D-095): a missing name is created; a name with the same value is skipped (no new version); a name with another value is 409 `secret-conflict` with the list of names, and nothing is written, unless `overwrite` lists it. Created and overwritten names are in `Plan.warnings`. A wrong passphrase is 422 and writes nothing.
- [x] The import Plan follows D-099: when it adds a secret binding or changes a `Rule`, only a human approves it.
- [x] A reference to a missing secret is 422 with the list of names.
- [x] Round trip: export → fresh store → import → approve gives the same document hashes. With the bundle, the secrets resolve to the same values.
- [x] The passphrase and the bundle plaintext are in no log, event or error.
- [x] Stays inside approved decisions (D-094, D-095). `go.yaml.in/yaml` and `golang.org/x/crypto` only.

## Tests
- Golden file for the format; round trip in both modes; a 9 MiB body, an alias bomb and a truncated bundle are refused; changed bundle ciphertext fails; a bundle with memory 4 GiB or time 1 is refused without argon2 work.
- Secrets: import over a live store with one equal, one different and one new secret → 409 and no write; with `overwrite: [different]` → written, version changes, a pending Plan that used it is stale; a name in `overwrite` that is not in the bundle is 422.
- An `apply` token cannot approve an import Plan that brings a `Rule` or a new secret binding.
- `make lint test` passes.

## Out of scope
- Scheduled backups, Proxmox Backup Server, export of history, adoption of existing guests after a restore (later; needs the host). UI (T-035).

## Result (owner, max 8 lines)
- Branch `pl-backend/T-033-export-import`, stacked on T-029 (#49, which merges T-032). Tests: `make lint test` → pass. D-150 … D-155.
- `state/export.go` (encode, decode, limits) + golden `testdata/export/basic.yaml`; `secret/bundle.go`; `plan/import.go`; `api/export.go`.
- Tests: golden, stable bytes, round trip with tricky strings, decode refusals (alias bomb, merge keys, tags, 9 MiB), bundle KDF refused without work, API round trip into a fresh store with the bundle, modes, refusals, secret conflicts with overwrite and stale Plan, human-only for a Rule and a new binding.

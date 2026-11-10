# T-033 – Export and import of the state

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-029, T-031, T-032

## Goal
A user can export the desired state as one readable YAML file and import it into this or a fresh Plumb through a Plan.

## Context (read only these)
- `docs/DECISIONS.md` D-010, D-030, D-094, D-095 · `docs/ARCHITECTURE.md` §12
- `internal/state/state.go`, `commit.go` · `internal/secret/*` · `internal/plan/plan.go` · `api/openapi.yaml` – `/export`, `/import`

## Files
`internal/state/export.go` (encode, decode, limits) · `internal/secret/bundle.go` (passphrase bundle) · `internal/api/*` · `internal/plan/*` (origin `import`) · tests, `internal/state/testdata/export/*`

## Acceptance criteria
- [ ] Export format of D-094. The output is stable: the same revision gives the same bytes, except the time in the header.
- [ ] `GET /export` (tier `read`) holds no secret value. `POST /export` (human session, passphrase ≥ 12 characters) adds the `SecretBundle`.
- [ ] `POST /import`: modes `replace` and `merge`, Plan with origin `import`, limits of D-095 (size, anchors, aliases, unknown `format`), 422 with pointers for an invalid document.
- [ ] With a passphrase the bundle secrets are written first and named in `Plan.warnings`. A wrong passphrase is 422 and writes nothing.
- [ ] A reference to a missing secret is 422 with the list of names.
- [ ] Round trip: export → fresh store → import → approve gives the same document hashes. With the bundle, the secrets resolve to the same values.
- [ ] The passphrase and the bundle plaintext are in no log, event or error.
- [ ] Stays inside approved decisions (D-094, D-095). `go.yaml.in/yaml` and `golang.org/x/crypto` only.

## Tests
- Golden file for the format; round trip in both modes; a 9 MiB body, an alias bomb and a truncated bundle are refused; changed bundle ciphertext fails.
- `make lint test` passes.

## Out of scope
- Scheduled backups, Proxmox Backup Server, export of history, adoption of existing guests after a restore (later; needs the host). UI (T-035).

## Result (owner, max 8 lines)
branch · commit · tests · notes

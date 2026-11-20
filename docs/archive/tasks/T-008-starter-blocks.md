# T-008 – Starter Blocks for the slice

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S (Codex)   · **Depends on:** T-003   · **Parallel with:** T-004, T-005, T-007
- **Status:** review fixes ready; PR #6

## Goal
The user can pick three useful Blocks in the first slice.

## Context (read only these)
- `docs/ARCHITECTURE.md` §3
- `internal/state/schema` and `internal/resource` schemas (from T-003)
- Files to add: `blocks/deploy-user.yaml`, `blocks/motd.yaml`, `blocks/nginx.yaml`, `blocks/blocks_test.go`

## Acceptance criteria
- [x] `deploy-user`: user with shell and SSH keys. `motd`: one file. `nginx`: package, one site file, and a service with `restart_on` for that file.
- [x] Every parameter has a type, a title and, where sensible, a default.
- [x] Only the five slice resource types and plain value templates are used. No `command` resource.
- [x] Tests: `go test ./blocks/...` loads every Block and validates it against the Block schema and the resource schemas.

## Out of scope
- More Blocks, TLS for Nginx.

## Plan (architect, optional)

## Result (owner, max 8 lines)
`pl-codex-fix/T-008-starter-blocks` · PR #6; includes QA commit `1bec4cb` (cherry-picked).
Tests: `go test ./blocks/...`, `make lint test`, and `git diff --check` pass.
Tests compile parameter schemas, validate defaults/sample inputs, render with missing-key errors,
validate resource schemas, and check metadata, plain templates, and restart references.
Negative tests reject root/invalid usernames, invalid SSH keys, nginx injection, and oversized input.
Mutation checks reject undefined parameters, wrong default types, missing titles, and dangling restarts.
All review must/should/nit items addressed, including explicit file ownership and final newline.
Follow-up: add a config-test step before nginx restart before adding more nginx options.

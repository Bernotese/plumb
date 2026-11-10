# T-008 – Starter Blocks for the slice

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S (Codex)   · **Depends on:** T-003   · **Parallel with:** T-004 … T-007, T-009, T-012
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
The user can pick three useful Blocks in the first slice.

## Context (read only these)
- `docs/ARCHITECTURE.md` §3
- `internal/state/schema` and `internal/resource` schemas (from T-003)
- Files to add: `blocks/deploy-user.yaml`, `blocks/motd.yaml`, `blocks/nginx.yaml`, `blocks/blocks_test.go`

## Acceptance criteria
- [ ] `deploy-user`: user with shell and SSH keys. `motd`: one file. `nginx`: package, one site file, service that restarts when the file changes.
- [ ] Every parameter has a type, a title and, where sensible, a default.
- [ ] Only the five slice resource types are used. No `command` resource.
- [ ] Tests: `go test ./blocks/...` loads every Block and validates it against the Block schema and the resource schemas.

## Out of scope
- More Blocks, TLS for Nginx.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-004 – State and store

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (Claude)   · **Depends on:** T-003   · **Parallel with:** T-005 … T-009, T-012
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
Plumb can store documents as revisions and can render a Target into the flat resource list that the agent applies.

## Context (read only these)
- `docs/ARCHITECTURE.md` §3
- `internal/state/schema`, `internal/wire`, `internal/store/migrations` (from T-003)

## Acceptance criteria
- [ ] `store`: opens the database (WAL, foreign keys on), runs migrations, and has typed methods for documents and revisions.
- [ ] `state`: validates `Image`, `Block` and `Target` documents. Block parameters get their defaults. An unknown Block, image or parameter is an error with a clear path.
- [ ] Render: templates, typed single references, `when`, `each`, `uses`, `after`, `overrides`. A duplicate resource id or a `uses` cycle is an error.
- [ ] Templates have no access to files, environment or network. Only an allowlist of functions is available.
- [ ] Commit: a revision stores documents by content hash. Unchanged documents are shared. Revisions are never changed.
- [ ] `Diff(a, b)` lists added, changed and removed documents between two revisions.
- [ ] Tests: `go test ./internal/state/... ./internal/store/...` passes. Golden tests render at least three Blocks from `testdata`.

## Out of scope
- Plans, runs, issues (T-010).
- Export/import, secrets, rules.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

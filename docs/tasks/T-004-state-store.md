# T-004 – State and store

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (Claude)   · **Depends on:** T-003   · **Parallel with:** T-005, T-007, T-008 (then T-009 by the same owner)
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
Plumb can store documents as revisions and can render a Target into the flat resource list that the agent applies.

## Context (read only these)
- `docs/ARCHITECTURE.md` §3
- `internal/state/schema`, `internal/wire`, `internal/store/migrations` (from T-003)

## Acceptance criteria
- [x] `store`: opens the database (WAL, foreign keys on), runs migrations, and has typed methods for documents and revisions.
- [x] `state`: validates `Image`, `Block` and `Target` documents. Block parameters get their defaults. An unknown Block, image or parameter is an error with a clear path.
- [x] Render: value templates and `overrides`. A duplicate resource id is an error. Two uses of one Block without different `id`s are an error (D-048). A `restart_on` id that does not exist on the target is an error.
- [x] Templates have no access to files, environment or network. Only an allowlist of functions is available.
- [x] Rendering stops with an error after a time limit and an output size limit. A test proves it with `{{ range }}` over a huge loop.
- [x] Commit: a revision stores documents by content hash (`state.Canonical` and `state.Hash` from T-003; never another form). Unchanged documents are shared. Revisions are never changed.
- [x] `Diff(a, b)` lists added, changed and removed documents between two revisions.
- [x] Tests: `go test ./internal/state/... ./internal/store/...` passes. Golden tests render at least three Blocks from `testdata`.
- [x] From the T-003 re-review: `state.Canonical` must not produce a spurious doc change for number text vs. integer (e.g. `2048.0` vs `2048`). Validate the canonical form and add a test.

## Out of scope
- `when`, `each`, `uses`, `after`, typed values (ARCHITECTURE §10).
- Plans, runs, issues (T-010).
- Export/import, secrets, rules.

## Plan (architect, optional)

## Result (owner, max 8 lines)
`pl-backend/T-004-state-store` · tests: `make lint test build` → pass (no `-race`: no C compiler on the VM).
- store: `Open` (WAL, foreign keys, busy timeout, BEGIN IMMEDIATE), user_version migrations, `CommitRevision`, `Head`, `Revision(s)`, `RevisionDocs`, `RevisionDoc`, `Doc`, `Diff` (0 = empty state).
- Migration 0002: revisions and doc rows immutable by triggers (D-051). Existing revision test adapted to it.
- state: `Validate` → `*Set` (schemas, refs, params with defaults, every Target renders), `Render`, `Commit`, `Load`. Errors carry kind, name and a JSON pointer.
- Sandbox and limits (D-052): allowlist; `$ref` loader blocked; numbers have no methods in templates; budget per Target (2 s, 1 MiB) plus a total per `Validate`/`Load` (10 s, 32 MiB, own error `ErrTotalBudget`). Tests stop huge, nested and writing loops, a doubling variable, and 10 slow Targets at the total.
- Review fixes: invalid Block/Image is "invalid", not "does not exist"; `Set.Docs` copies specs (QA PR #9); end-to-end test 2048.0 vs 2048 in the store; `Revisions` limit clamped.
- Canonical numbers normalised (D-050): `2048.0` = `2048`; the pinned hash in `TestCanonical` changed.
- Golden tests: `internal/state/testdata` renders deploy-user, nginx, motd (two Targets); `go test ./internal/state -update` rewrites.

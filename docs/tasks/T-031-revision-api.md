# T-031 – Revision history: list, diff, rollback API

- **Owner:** pl-reviewer (as implementer; review by pl-architect)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-027

## Goal
A client can read the revision history, compare two revisions and roll back to one through a Plan.

## Context (read only these)
- `docs/DECISIONS.md` D-022, D-093, D-096 · `api/openapi.yaml` – `/revisions` operations
- `internal/store/revision.go` (`Revisions`, `Diff`, `RevisionDocs`), `internal/plan/plan.go` (`Proposal`, origins), `internal/api/handlers.go`

## Files
`internal/api/*` (handlers, views) · `internal/plan/*` (rollback proposal, origin) · `internal/store/migrations/0004_plan_origin.sql` · tests

## Acceptance criteria
- [ ] `GET /revisions` and `GET /revisions/{n}/diff` per the contract. The diff holds both document versions; unknown `n` or `from` is 404.
- [ ] `POST /revisions/{n}/rollback` builds a proposal that puts and deletes documents until the set equals revision `n`, and returns a Plan with origin `rollback`. The Plan follows every normal rule (stale, tiers, risk, create-only fields).
- [ ] Rollback to a set that equals the head is 409 `nothing-to-do`.
- [ ] After approval the new revision has the same document hashes as revision `n`.
- [ ] Migration 0004 adds the origins `rollback`, `import`, `ignore_rule` (table rebuild; existing rows and foreign keys survive; a test migrates a database with Plans, Runs and Issues).
- [ ] Stays inside approved decisions (D-093, D-096).

## Tests
- Slice test: three revisions, diff 1→3, rollback to 1, approve, compare hashes; rollback that deletes a target is `destructive` and a token cannot approve it.
- `make lint test` passes.

## Out of scope
- UI (T-034). History of secret values (none, D-093).

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-031 – Revision history: list, diff, rollback API

- **Owner:** pl-backend (from pl-reviewer, handover a7db012; review by pl-architect)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-027

## Goal
A client can read the revision history, compare two revisions and roll back to one through a Plan.

## Context (read only these)
- `docs/DECISIONS.md` D-022, D-093, D-096, D-099 · `api/openapi.yaml` – `/revisions` operations
- `internal/store/revision.go` (`Revisions`, `Diff`, `RevisionDocs`), `internal/plan/plan.go` (`Proposal`, origins), `internal/api/handlers.go`

## Files
`internal/api/*` (handlers, views) · `internal/plan/*` (rollback proposal, origin) · `internal/store/migrations/0004_plan_origin.sql` · tests

## Acceptance criteria
- [x] The operations of this card lose `x-plumb-planned` in `api/openapi.yaml` and get their rows in the tier matrix of `internal/api` (D-100).
- [x] `GET /revisions` and `GET /revisions/{n}/diff` per the contract. The diff holds both document versions; unknown `n` or `from` is 404.
- [x] `POST /revisions/{n}/rollback` builds a proposal that puts and deletes documents until the set equals revision `n`, and returns a Plan with origin `rollback`. The Plan follows every normal rule (stale, tiers, risk, create-only fields).
- [x] Rollback to a set that equals the head is 409 `nothing-to-do`.
- [x] The rollback goes through the same `plan.Propose` as every proposal and skips no check. The human-only rules of D-099 read the content of the proposal, so they hold for a rollback; the cards that add them (T-029, T-032) add the rollback tests.
- [x] After approval the new revision has the same document hashes as revision `n`.
- [x] Migration 0004 adds the origins `rollback`, `import`, `ignore_rule` (table rebuild; existing rows and foreign keys survive; a test migrates a database with Plans, Runs and Issues).
- [x] Stays inside approved decisions (D-093, D-096).

## Tests
- Slice test: three revisions, diff 1→3, rollback to 1, approve, compare hashes; rollback that deletes a target is `destructive` and a token cannot approve it.
- `make lint test` passes.

## Out of scope
- UI (T-034). History of secret values (none, D-093).

## Result (owner, max 8 lines)
- Branch `pl-backend/T-031-revision-api`, stacked on T-028 (PR #37): migration 0004 needs 0003 (D-096, no gaps D-051). Tests: `make lint test` → pass.
- From the pl-reviewer handover (kept whole): runner D-115, migration 0004, `plan.Rollback`, the three handlers, matrix rows.
- Added: slice test `TestRevisionHistoryAndRollback`, TestContract calls (and `rule.json` in the contract loader), migration test through 0003 + 0004, D-115, D-125 … D-127.
- Human-only rollback tests (D-099) come with T-029 and T-032.

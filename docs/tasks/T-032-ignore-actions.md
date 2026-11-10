# T-032 – Issues: "Ignore once" and "Ignore by rule"

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-027, T-031 (migration order)

## Goal
A user can ignore one drift, or ignore a kind of drift for good with a `Rule` document. The agent does not change.

## Context (read only these)
- `docs/DECISIONS.md` D-044, D-068, D-091, D-092, D-096 · `docs/ARCHITECTURE.md` §6, §12
- `internal/issue/issue.go` (`Intake`, `Resolve`, `PlanDone`), `internal/state/state.go`, `validate.go`, `internal/state/schema/rule.json` (T-027)
- `api/openapi.yaml` – `resolveIssue`, `/rules`

## Files
`internal/issue/*` · `internal/state/*` (kind `Rule`, validation) · `internal/store/issue.go`, `migrations/0005_issue_ignore.sql` · `internal/api/*` (`/rules`, resolve answers) · `internal/plan/*` (origin `ignore_rule`, `Change.ignored` in Plans) · tests

## Acceptance criteria
- [ ] `ignore_once` (D-091): 200 with the Issue, `resolved`, resolution `ignore`, no Plan, no revision. An equal later report opens no Issue. A report with other changes opens one. After `unchanged` or an apply of the resource, the same drift opens an Issue again.
- [ ] `ignore_rule` (D-092): 201 with a Plan (origin `ignore_rule`, risk `read`, no target change) that puts one `Rule`. The Issue is `resolving`; it resolves as `ignore` when the revision commits and is `open` again when the Plan is dropped or stale.
- [ ] `Rule` is a document kind: schema check, `target` is an existing Target or `*`, export order, history, rollback, delete by proposal. `GET /rules` and `GET /rules/{name}` per the contract.
- [ ] Intake drops changes that a rule of the head revision matches (target, resource id, attrs). A report with no change left opens no Issue. A new rule resolves matching open Issues.
- [ ] A Plan still shows and applies a difference that a rule matches, with `ignored: true` on the change.
- [ ] A sensitive change can be ignored (the match uses `attr` only).
- [ ] `Issue.actions` lists `ignore_once` and `ignore_rule` for an open Issue. Tier `plan` for both.
- [ ] Migration 0005: resolution `ignore` and the ignore memory (table rebuild as in T-031).
- [ ] Stays inside approved decisions (D-091, D-092). It replaces D-044.

## Tests
- Slice tests with `linktest` drift: every bullet above; a rule for `*`; rule removed → the drift opens an Issue again; crash between Plan and commit leaves the Issue `open` or `resolving`, never lost.
- `make lint test` passes.

## Out of scope
- Rules with patterns, strictness settings per resource, agent-side filtering. UI (T-036).

## Result (owner, max 8 lines)
branch · commit · tests · notes

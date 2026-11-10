# T-032 – Issues: "Ignore once" and "Ignore by rule"

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-027, T-031 (migration order)

## Goal
A user can ignore one drift, or ignore a kind of drift for good with a `Rule` document. A token cannot hide drift that it could not revert. The agent does not change.

## Context (read only these)
- `docs/DECISIONS.md` D-044, D-046, D-068, D-091, D-092, D-096, D-099 · `docs/ARCHITECTURE.md` §6, §12
- `internal/issue/issue.go` (`Intake`, `Resolve`, `PlanDone`), `internal/state/state.go`, `validate.go`, `internal/state/schema/rule.json` (T-027)
- `api/openapi.yaml` – `resolveIssue`, `/rules`

## Files
`internal/issue/*` · `internal/state/*` (kind `Rule`, validation) · `internal/store/issue.go`, `migrations/0005_issue_ignore.sql` · `internal/api/*` (`/rules`, resolve answers, approval check, `ignored_drift` in the target view) · `internal/plan/*` (origin `ignore_rule`, `human_only: rule-change`, `Change.ignored` in Plans) · tests

## Acceptance criteria
- [ ] The operations of this card lose `x-plumb-planned` in `api/openapi.yaml` and get their rows in the tier matrix of `internal/api` (D-100).
- [ ] Who may `ignore_once` (D-091): a human session; an `apply` token only when every change of the Issue has a risk up to its `max_risk` and none is sensitive. A `plan` or `read` token gets 403 `human-required`; an `apply` token over its limit gets 403 `risk-too-high`; a sensitive or `destructive` change is human-only.
- [ ] `ignore_once` (D-091): 200 with the Issue, `resolved`, resolution `ignore`, no Plan, no revision. An equal later report opens no Issue. A report with other changes opens one. After `unchanged` or an apply of the resource, the same drift opens an Issue again.
- [ ] `ignore_rule` (D-092): 201 with a Plan (origin `ignore_rule`, risk `read`, no target change) that puts one `Rule`. The Issue is `resolving`; it resolves as `ignore` when the revision commits and is `open` again when the Plan is dropped or stale.
- [ ] Human-only rule (D-099): a Plan that puts, changes or deletes a `Rule` has `human_only: [rule-change]`, for every origin (issue action, plain proposal, rollback, import). A token that approves gets 403 `human-required`. A `plan` token may still propose it.
- [ ] `Rule` is a document kind: schema check, `target` is an existing Target or `*`, export order, history, rollback, delete by proposal. `GET /rules` and `GET /rules/{name}` per the contract.
- [ ] Intake drops changes that a rule of the head revision matches (target, resource id, attrs). A report with no change left opens no Issue. A new rule resolves matching open Issues.
- [ ] Every drop is recorded per target and resource (rule name, attributes, last time; no values). The target view returns it as `ignored_drift`. The record goes away when the resource reports `unchanged` or the rule is removed.
- [ ] A Plan still shows and applies a difference that a rule matches, with `ignored: true` on the change.
- [ ] A sensitive change can be ignored (the match uses `attr` only).
- [ ] `Issue.actions` lists `ignore_once` and `ignore_rule` for an open Issue. The list does not depend on the caller; the server enforces the rules above.
- [ ] Migration 0005: resolution `ignore`, the ignore memory and the record of dropped drift (table rebuild as in T-031).
- [ ] Stays inside approved decisions (D-091, D-092, D-099). They replace D-044.

## Tests
- Tier table for `ignore_once` (read, plan, apply `reversible`, apply `disruptive`, human × reversible, disruptive, destructive, sensitive change). An `apply` token cannot approve a Plan that adds a `Rule` for `*`, changes a rule, or deletes one, by proposal and by rollback.
- Slice tests with `linktest` drift: every bullet above; a rule for `*`; rule removed → the drift opens an Issue again; crash between Plan and commit leaves the Issue `open` or `resolving`, never lost.
- `make lint test` passes.

## Out of scope
- Rules with patterns, strictness settings per resource, agent-side filtering. UI (T-036).

## Result (owner, max 8 lines)
branch · commit · tests · notes

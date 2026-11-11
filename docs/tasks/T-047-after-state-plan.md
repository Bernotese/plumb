# T-047 – `after` and `restarts` in render and Plan

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-051)
- **Size:** M   · **Depends on:** T-046   · **Parent:** T-044

## Goal
A Block with `spec.after` or `spec.restarts` renders, bad names and cycles are save-time errors, and the Plan carries `after` and `restart_by`.

## Context (read only these)
- `docs/tasks/T-044-block-order-nginx-site.md` – Plan sections 2, 3, 5, 7, 8
- `docs/decisions/T-044.md` – D-168, D-169, D-171, D-172
- `internal/wire/order.go` – `Order`, `OrderError`, `RestartOn` (use them; do not write a second graph)
- `internal/state/validate.go`, `internal/state/render.go`, `internal/plan/propose.go` (`planTarget`, `plannedRestarts`), `internal/api/views.go`, `internal/resource/service.go` (`RestartChange`), `cmd/plumb/dev.go`

## Acceptance criteria
- [ ] Remove the "not available yet" guard of T-046 in `validate.go`. Block check: the type of each `after` id is an agent resource type (not `guest`).
- [ ] Render order (D-172): Blocks, `restarts`, overrides, type schemas, `restart_on` check, `after` check, cycle check. `After` is set on the first resource of a use only.
- [ ] The four error cases of Plan section 2, with the given paths and texts. The cycle text comes from `wire.OrderError`.
- [ ] **S2:** `restarts` appends `any` values, so `restart_on` stays `[]any`. `wire.RestartOn` reads the list in render, `plannedRestarts` and `resource.RestartChange` (remove `toStrings` use there). Test: an existing Target with `nginx` gets a site with a new secret binding; `service:nginx` still gets the planned restart (D-160).
- [ ] **S3:** the cycle check runs in `state.Validate` of a proposed state (proposal, rollback, import). `state.Load` of a stored revision does not fail on a cycle; it logs one warning with the Target name. Test: a stored revision with a `restart_on` cycle loads; a proposal that does not repair it is refused and names the Target; a proposal that repairs it passes.
- [ ] Plan: `ResourceChange.After` (from the render) and `RestartBy` (ids from `restart_on` that change in this Plan or are left out of the observe, in list order) → API view. Contract test covers both fields.
- [ ] Dev seed (`cmd/plumb/dev.go`): an inline `nginx-site` Block with `after` and `restarts`, and one Target that uses it.
- [ ] Tests: `internal/state` and `internal/api` cases of Plan section 8; `make lint test` passes.

## Out of scope
- Agent (T-048), web (T-049), `blocks/nginx-site.yaml` (T-050), guest and slice tests (T-051).
- Decisions: none expected. A new one needs a number from pl-lead.

## Result (owner, max 8 lines)
branch · commit · tests · notes
pl-architect/T-047-after-state-plan · tests: `make lint test` → pass.
Render: `restarts` (list stays `[]any`), overrides, schemas, `restart_on` check, `after` check, `wire.Order`. The T-046 guard is gone. `wire.RestartOn` reads the list in render, `plannedRestarts` and `RestartChange`.
S3: `state.Load` keeps a stored cycle (`Set.Cycles`); the engine logs one warning per revision and Target (`Engine.load`); `Validate` refuses it. Tests: `TestLoadKeepsAStoredCycle`, `TestStoredCycleIsServedAndMustBeRepaired`.
S2: `TestRestartsWithANewSecretBinding` (planned restart of D-160 with `restart_by`, Run succeeds). Plan fields: `TestPlanShowsAfterAndRestartBy`, `TestPlanShowsAfterOnANewTarget`, both checked against `api/openapi.yaml`.
Review nits of T-046: the cycle hint names the order of the resources in a Block (N1); `RestartOn` returns a copy (N3). N4 (`minItems`) not taken: a contract change with no effect.
Dev seed: `nginx-site` on `web2` (the e2e tests edit `web1`). Mutations (local): 6, all caught (PR text).

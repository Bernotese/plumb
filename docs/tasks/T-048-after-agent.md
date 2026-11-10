# T-048 – Agent: `after` edges and the skip rule

- **Owner:** tbd (pl-lead assigns)   · **Reviewer:** pl-reviewer (focus: skip rule, no deadlock)   · **QA:** pl-codex-qa (T-051)
- **Size:** S–M   · **Depends on:** T-046   · **Parent:** T-044

## Goal
The agent runs a Block use only after the resources that it comes after, and skips it with a clear cause when one of them did not succeed.

## Context (read only these)
- `docs/tasks/T-044-block-order-nginx-site.md` – Plan sections 4, 8
- `docs/decisions/T-044.md` – D-170
- `internal/wire/order.go` – `Order` (edges with kind), `RestartOn`
- `internal/agent/exec.go`, `internal/agent/agent_test.go` (`TestApplyOrderRiskAndRestart`, `TestPlanOrder`), `internal/agent/planned_test.go`

## Acceptance criteria
- [ ] `agent.plan` builds its steps from `wire.Order`; its own cycle code and `restartOn` are gone. A duplicate id, an unknown `after` id and a cycle fail the apply before anything runs.
- [ ] **S1:** a step has two lists: `needs` (`after` edges) and `restartDeps` (`restart_on` edges). Only `restartDeps` feed `resource.RestartChange`. Test: a service is the first resource of a use with `after: [package:x]`; `package:x` is applied; no restart.
- [ ] A step waits for its `needs` before it observes. All ended `unchanged` or `applied` → it runs. One ended `failed`, `refused` or `skipped` → `skipped`, output `not applied: <id> was <status>`; the rest of the use is skipped. `restart_on` keeps its rule: the service waits and still runs after a failed resource.
- [ ] **N1:** every skipped step has an output that names the cause, also after a failure inside its own use.
- [ ] **S5:** the context ends during the wait → the step and the rest of its use are `skipped`, output `not applied: the apply ended while this resource waited for <id>`; `apply` returns and no goroutine is left. Test with a dependency that blocks.
- [ ] Tests (Plan section 8, `internal/agent`): order; a use with no relation runs while the dependency blocks; `failed` / `refused` / `skipped` dependency; chain of three uses; service with `restart_on` on a skipped resource still runs; both list orders. `go test -count=20 ./internal/agent` and `make lint test` pass.
- [ ] Update the `apply` and `plan` comments in `exec.go` ("Blocks run in parallel").

## Out of scope
- `linktest` fake agent: no change (it applies in list order and has no skip rule).
- Render, Plan, UI. A version gate for old agents (D-169, D-176).

## Result (owner, max 8 lines)
branch · commit · tests · notes

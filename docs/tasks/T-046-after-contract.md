# T-046 – Contract for Block order (`after`, `restarts`)

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-044 plan approved (D-176)   · **Parent:** T-044

## Goal
The contract of T-044 is on `main`, so T-047, T-048 and T-049 can run in parallel against it.

## Context (read only these)
- `docs/tasks/T-044-block-order-nginx-site.md` – Plan sections 2, 3, 9
- `docs/decisions/T-044.md` – D-167 … D-175

## Acceptance criteria
- [ ] Child cards T-046 … T-051 are in `docs/tasks/`.
- [ ] `api/openapi.yaml`: `ResourcePlan.after`, `ResourcePlan.restart_by`, text of `StepStatus` (`skipped`).
- [ ] `internal/state/schema/block.json`: `spec.after`, `spec.restarts`. Until T-047 a Block that uses one of them is refused ("not available yet"), so no state says `after` and applies without it.
- [ ] `internal/wire`: `Resource.After`; `Order` (edges with kind: `block`, `restart_on`, `after`; duplicate, unknown `after` id, cycle) and `RestartOn`, with tests; `StatusSkipped` comment (N3).
- [ ] `docs/ARCHITECTURE.md` §3 and §10 (agent self-update owns the version gate). `docs/decisions/T-044.md`: decided.
- [ ] Tests: `make lint test` passes; `npm run gen` and `npm run check:api` pass.

## Out of scope
- Render, plan, agent, UI, the `nginx-site` Block (T-047 … T-051). `agent.plan` still has its own graph until T-048.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-010 – Plan, apply and issues

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (Claude)   · **Depends on:** T-004, T-009, T-018   · **Parallel with:** T-006
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
A proposal becomes a Plan, an approved Plan becomes a Revision and a Run, and drift becomes an Issue that the user can accept or revert.

## Context (read only these)
- `docs/ARCHITECTURE.md` §4, §5, §6
- `internal/state`, `internal/link`, `internal/driver`, `internal/agent` (from T-004, T-009, T-018)

## Acceptance criteria
- [ ] `plan`: proposal → validate → render → observe (agents and driver) → Plan with changes and risk. `restart` changes are part of the Plan.
- [ ] The Plan fails with a clear reason only when a target that the proposal touches is offline. Other offline targets do not matter.
- [ ] A proposal that changes a create-only field of an existing target (`ip`, bootstrap user, `disk`) is refused with a clear message.
- [ ] A new target plans `guest:config` as create and lists its resources as "unverified until first boot".
- [ ] Every agent report goes through `wire.Redact` before it is stored or sent on. A test sends a report with values for a sensitive attribute and finds no value in the database or in events.
- [ ] Approve: fails when the Plan is stale. It takes a check function for the rights of the principal (used by T-011).
- [ ] Approve commits the Revision with author, reason and plan id, then starts the Run.
- [ ] Run: targets in parallel. Per target: guest step, wait for enrollment, resource steps. Every step emits an event. A failed step does not stop other targets.
- [ ] Re-enroll: a Plan with risk `disruptive` sets a new token and restarts the guest. A target whose token expired before the first boot becomes usable again.
- [ ] `issue`: a drift report opens one Issue per target and resource, or updates the open one. `revert` creates a Plan. `accept` creates a Plan that writes an override. `accept` is refused for a sensitive attribute.
- [ ] Proxmox level: a poll loop calls `Driver.Diff` when the config digest changed and reports drift on `guest:config`.
- [ ] Tests: `go test ./internal/...` passes. One in-process test uses `driver/fake` and the real agent code: create target → approve → apply → change a file → Issue opens → `revert` closes it.
- [ ] From the T-009 review (D-062): back `link.Config.Sensitive` with the head revision, cached per revision id. Test: after a controller restart, a drift report is still redacted.

## Out of scope
- HTTP handlers and auth (T-011).
- `ignore_once`, `ignore_rule`, rollback, import.

## Plan (architect, optional)

## Notes
- From the T-009 review (nit 9): a command that link dequeued but could not deliver is lost. The Run fails its steps after the apply timeout; the user plans again (D-067).

## Result (owner, max 8 lines)
`pl-backend/T-010-plan-apply-issues` · tests: `make lint test build` → pass locally; `-race` runs in CI.
- plan: Propose (touched targets only, D-065; new target unverified, D-066; create-only refused), Approve (check func, one-tx stale check + revision + run), Run (targets in parallel, guest step, wait, apply, watch; events per step, D-067), re-enroll (D-069), CheckGuests/PollGuests, `Sensitive` from the head render.
- issue: Intake (one Issue per target+resource, gone), Resolve revert/accept (D-068), PlanDone.
- store: plans, runs, steps, issues, bindings; ApprovePlan in one transaction.
- Tests use driver/fake + the real link + `link/linktest` (a fake agent that speaks the wire protocol): create → approve → apply → drift → revert, accept, gone, offline, create-only, stale, failed target, restart change, re-enroll after token expiry, guest drift, no secret in DB/events also after a controller restart.
- **Waits for T-018:** the same flow test with the real agent code (`internal/agent`) instead of `linktest`; `Resource.Sensitive` lists come with the secret store, so the head-revision source of D-062 is tested only with empty lists.

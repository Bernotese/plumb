# T-010 – Plan, apply and issues

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (Claude)   · **Depends on:** T-004, T-005, T-009   · **Parallel with:** T-013
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
A proposal becomes a Plan, an approved Plan becomes a Revision and a Run, and drift becomes an Issue that the user can accept or revert.

## Context (read only these)
- `docs/ARCHITECTURE.md` §4, §6
- `internal/state`, `internal/link`, `internal/driver`, `internal/agent` (from T-004, T-005, T-009)

## Acceptance criteria
- [ ] `plan`: proposal → validate → render → observe (agents and driver) → Plan with changes and risk. An offline target makes the Plan fail with a clear reason.
- [ ] A new target plans `guest:config` as create and lists its resources as "unverified until first boot".
- [ ] Approve: fails when the Plan is stale. It takes a check function for the rights of the principal (used by T-011).
- [ ] Approve commits the Revision with author, reason and plan id, then starts the Run.
- [ ] Run: targets in parallel. Per target: guest step, wait for enrollment, resource steps. Every step emits an event. A failed step does not stop other targets.
- [ ] `issue`: a drift report opens one Issue per target and resource, or updates the open one. `revert` creates a Plan. `accept` creates a Plan that writes an override.
- [ ] Proxmox level: a poll loop compares managed keys when the config digest changed and reports drift on `guest:config`.
- [ ] Tests: `go test ./internal/...` passes. One slice test uses `driver/fake` and an in-process agent: create target → approve → apply → change a file → Issue opens → `revert` closes it.

## Out of scope
- HTTP handlers and auth (T-011).
- `ignore_once`, `ignore_rule`, rollback, import.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

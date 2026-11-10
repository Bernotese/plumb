# T-009 – Agent link, events and fake driver

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-003 (after T-004, same owner)   · **Parallel with:** T-005, T-018
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
The controller can enroll agents, send them commands and receive reports. A fake driver stands in for Proxmox.

## Context (read only these)
- `docs/ARCHITECTURE.md` §2, §5, §7
- `internal/wire`, `internal/driver` (from T-003)

## Acceptance criteria
- [ ] `link`: creates the Plumb CA and the server certificate at first start (key files mode 0600). It serves `/agent/v1` on its own listener.
- [ ] Enrollment token: random 256 bit, bound to one target, single use, 15 minutes, stored as a hash. A wrong, used or expired token gives the same error.
- [ ] A new token for a target makes the old token and the old credential of that target invalid (re-enrollment).
- [ ] After enrollment every call needs the per-agent credential. Comparison is constant-time. A credential works for one target only.
- [ ] `GET /agent/v1/binary` serves the embedded agent binary. It needs no credential and has a rate limit.
- [ ] Command queue per agent with long poll. A target is online when it polled in the last 90 s.
- [ ] Reports have a size limit and are validated against the `wire` types.
- [ ] `event`: append to the store and subscribe from a sequence number.
- [ ] `driver/fake`: guests in memory, async tasks, config digest, `Diff` with risks and the same create-only fields as the Proxmox driver. Tests can inject failures and outside changes.
- [ ] Tests: `go test ./internal/link/... ./internal/event/... ./internal/driver/...` passes. Negative tests: token reuse, expired token, token of another target, credential of another target, oversized report.

## Out of scope
- Plans and issues (T-010).
- LXC enrollment, agent update.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

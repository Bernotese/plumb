# T-009 – Agent link, events and fake driver

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-003   · **Parallel with:** T-004 … T-008, T-012
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
The controller can enroll agents, send them commands and receive reports. A fake driver stands in for Proxmox.

## Context (read only these)
- `docs/ARCHITECTURE.md` §2, §5, §7
- `internal/wire`, `internal/driver` (from T-003)

## Acceptance criteria
- [ ] `link`: creates the Plumb CA and the server certificate at first start (key files mode 0600). It serves `/agent/v1` on its own listener.
- [ ] Enrollment token: random 256 bit, bound to one target, single use, 15 minutes, stored as a hash. A wrong, used or expired token gives the same error.
- [ ] After enrollment every call needs the per-agent credential. Comparison is constant-time. A credential works for one target only.
- [ ] Command queue per agent with long poll. A target is online when it polled in the last 90 s.
- [ ] Reports have a size limit and are validated against the `wire` types.
- [ ] `event`: append to the store and subscribe from a sequence number.
- [ ] `driver/fake`: guests in memory, async tasks, config digest. Tests can inject failures and outside changes.
- [ ] Tests: `go test ./internal/link/... ./internal/event/... ./internal/driver/...` passes, including token reuse, expiry and a wrong credential.

## Out of scope
- Plans and issues (T-010).
- LXC enrollment, agent update.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

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
- [x] `link`: creates the Plumb CA and the server certificate at first start (key files mode 0600). It serves `/agent/v1` on its own listener.
- [x] Enrollment token: random 256 bit, bound to one target, single use, 15 minutes, stored as a hash. A wrong, used or expired token gives the same error.
- [x] A new token for a target makes the old token and the old credential of that target invalid (re-enrollment).
- [x] After enrollment every call needs the per-agent credential. Comparison is constant-time. A credential works for one target only.
- [x] `GET /agent/v1/binary` serves the embedded agent binary. It needs no credential and has a rate limit.
- [x] Command queue per agent with long poll. A target is online when it polled in the last 90 s.
- [x] Reports have a size limit and are validated against the `wire` types. `link` hands reports on only after `wire.Redact`.
- [x] `event`: append to the store and subscribe from a sequence number.
- [x] `driver/fake`: guests in memory, async tasks, config digest, `Diff` with risks and the same create-only fields as the Proxmox driver. Tests can inject failures and outside changes.
- [x] Tests: `go test ./internal/link/... ./internal/event/... ./internal/driver/...` passes. Negative tests: token reuse, expired token, token of another target, credential of another target, oversized report.

## Out of scope
- Plans and issues (T-010).
- LXC enrollment, agent update.

## Plan (architect, optional)

## Result (owner, max 8 lines)
`pl-backend/T-009-link-events-fake-driver` · tests: `make lint test build` → pass (no `-race`: no C compiler on the VM).
- link: CA + listener cert (D-064), enroll/poll/report/binary on `Handler`/`ServeListener` (own TLS 1.3 listener), `NewEnrollToken`, `Send`, `Online`, `OnReport` hook after validation + Redact (D-060..D-062).
- store: enroll tokens, agents, events; times now fixed-width UTC text, so SQL compares them correctly.
- event: `Append`/`Emit`, `Subscribe(after)` (replay from store, then live, no gaps).
- driver: shared `DiffGuest` + `CreateOnly` (D-063); `driver/fake` with async tasks, digest, `Fail`, `Tamper`, `Bootstrap`, token at next cold start.
- Negative tests: token reuse/expired/replaced/unknown (one answer), credential of another target, re-enrollment, oversized report (413), bad reports (400), foreign command id (409), binary rate limit.
- AC5 (real embedded agent binary in `cmd/plumb`) moved to T-011 by pl-lead; `link` serves `Config.AgentBinary`.
- Review fixes: queue bound to the current agent (must 1), D-062 three-source redaction with `Config.Sensitive` (must 2), CA half-missing error, dir mode, Output/Error masking, failed-auth limit, limiter cap, `Subscription.Err`, race in `driver/fake` tasks (found by the new CI race step). QA PR #12 merged; its revocation test passes.

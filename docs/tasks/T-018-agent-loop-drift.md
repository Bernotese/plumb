# T-018 – Agent loop and drift

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-003, T-005   · **Parallel with:** T-009
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
The agent can enroll, run commands from the controller and report drift.

## Context (read only these)
- `docs/ARCHITECTURE.md` §5, §6, §7
- `internal/wire`, `internal/resource` (from T-003, T-005)

## Acceptance criteria
- [ ] Enroll: URL and CA come from `/etc/plumb-agent/agent.json`. The token comes from a configurable source (default: `/sys/class/dmi/id/product_serial`). The agent stores the credential with mode 0600.
- [ ] After a successful enrollment the agent creates `/etc/cloud/cloud-init.disabled`.
- [ ] The agent refuses any TLS certificate that the pinned CA did not sign. A test uses a certificate from another CA.
- [ ] Poll loop with backoff. It handles `observe`, `apply` and `watch`. `apply` observes again and refuses a change above `max_risk`.
- [ ] Order: listed order inside a Block, Blocks in parallel. A `restart` change is added when a `restart_on` resource changed, and runs after it.
- [ ] Drift: inotify on parent directories, 2 s debounce, full check every 15 minutes (configurable). The agent reports only differences.
- [ ] The agent's own apply creates no drift report. A test applies a file and expects zero reports; then it changes the file from outside and expects one.
- [ ] The desired list is on disk with mode 0600. The agent reports open drift again after a reconnect.
- [ ] Tests: `go test ./internal/agent/...` passes (agent against an `httptest` controller).

## Out of scope
- `update` and `seal` commands.
- LXC enrollment.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

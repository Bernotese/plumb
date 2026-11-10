# T-005 – Agent and first resources

- **Owner:** pl-reviewer (fallback implementer)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** L (Claude)   · **Depends on:** T-003   · **Parallel with:** T-004, T-006 … T-009, T-012
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
The agent can enroll, apply five resource types in a guest and report drift on them.

## Context (read only these)
- `docs/ARCHITECTURE.md` §2, §5, §6
- `internal/wire`, `internal/resource` (from T-003)

## Acceptance criteria
- [ ] `file`, `directory`, `package`, `service`, `user` implement `Resource`. A second apply reports no change.
- [ ] Every `Diff` sets a risk per change. `package` has strictness `present` (default) and `exact`.
- [ ] The root directory and the command runner are injected. Unit tests use a temp directory and a fake runner. No test needs root.
- [ ] Agent: reads `/etc/plumb-agent/enroll.json` (URL, CA, token), enrolls, stores the credential with mode 0600, and deletes the token.
- [ ] Agent: poll loop with backoff. It handles `observe`, `apply` and `watch`. `apply` observes again and refuses a change above `max_risk`.
- [ ] Order: listed order inside a Block, Blocks in parallel, `after` respected.
- [ ] Drift: inotify on parent directories, 2 s debounce, full check every 15 minutes (configurable). The agent reports only differences. It keeps the desired list on disk and reports again after a reconnect.
- [ ] The agent refuses any TLS certificate that the pinned CA did not sign.
- [ ] Tests: `go test ./internal/resource/... ./internal/agent/...` passes (agent against an `httptest` controller). `make test-guest` passes on Debian 13 and Ubuntu 26.04 (harness: T-007).

## Out of scope
- `update` and `seal` commands, other resource types.
- LXC enrollment.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

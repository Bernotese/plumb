# T-018 – Agent loop and drift

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-003, T-005   · **Parallel with:** T-009
- **Status:** review

## Goal
The agent can enroll, run commands from the controller and report drift.

## Context (read only these)
- `docs/ARCHITECTURE.md` §5, §6, §7
- `internal/wire`, `internal/resource` (from T-003, T-005)

## Acceptance criteria
- [ ] Enroll: URL and CA come from `/etc/plumb-agent/agent.json`. The token comes from a configurable source (default: `/sys/class/dmi/id/product_serial`). The agent stores the credential with mode 0600.
- [ ] After a successful enrollment the agent creates `/etc/cloud/cloud-init.disabled`.
- [ ] On a 401 the agent drops the old credential, reads the token source again and enrolls again, with backoff. It never stops and never loops on the old credential. A test rejects the credential, sets a new token and expects a new enrollment.
- [ ] The agent refuses any TLS certificate that the pinned CA did not sign. A test uses a certificate from another CA.
- [ ] Poll loop with backoff. It handles `observe`, `apply` and `watch`. `apply` observes again and refuses a change above `max_risk`.
- [ ] Order: listed order inside a Block, Blocks in parallel. A `restart` change is added when a `restart_on` resource changed, and runs after it.
- [ ] Drift: inotify on parent directories, 2 s debounce, full check every 15 minutes (configurable). The agent reports only differences.
- [ ] The agent's own apply creates no drift report. A test applies a file and expects zero reports; then it changes the file from outside and expects one.
- [ ] The desired list is on disk with mode 0600. The agent reports open drift again after a reconnect.
- [ ] Tests: `go test ./internal/agent/...` passes (agent against an `httptest` controller).
- [ ] From the T-009 re-review: status codes of the link: 401 = credential revoked (re-enroll from the token source); 429 and 503 = retry with backoff, never re-enroll. Test both.

## Out of scope
- `update` and `seal` commands.
- LXC enrollment.

## Plan (architect, optional)

Protocol status rule: 401 means revoked credentials and triggers re-enrollment.
429 (honor Retry-After) and 503 mean back off and retry with the current credential.

## Result (owner, max 8 lines)
- Branch `pl-frontend/T-018-agent-loop-drift` (based on T-005, main merged), PR below. `internal/agent`: `config.go`, `client.go` (pinned CA only), `store.go` (credential + desired list 0600 in a 0700 dir), `agent.go` (enroll, poll loop, backoff), `exec.go` (observe/apply/watch, order, restart, risk), `drift.go` + `inotify.go`. `plumb-agent run [-config]`. Rules: D-055.
- Output rule (T-005 review should 3): every `Item.Output` and resource error goes through `resource.SafeOutput`; an observed `State` never leaves the guest.
- D-075 (wire commit 56e0a68 cherry-picked): apply changes a resource only when `wire.Covers(planned, new difference)` or the entry is `unverified`, and within `max_risk`; else `refused` ("the guest differs from the approved Plan; plan again"), rest of the Block skipped, drift check reports it. Bad `planned` entries fail the command.
- Status codes: 401 → drop credential, enroll from the token source; 429/503/network → backoff, credential kept. Both tested.
- Tests: `go test ./internal/agent/...` pass against an `httptest` TLS controller (enroll + cloud-init flag + 0600, re-enroll after 401, foreign CA refused, observe with restart, apply order/risk/single restart, own apply = no drift then one drift then resolved, desired list on disk + resend after reconnect, 429/503, command errors, cycle check). Stable with `-count=5 -cpu 1,4`. Coverage 84 %.
- Not tested on a real guest (inotify on real `/etc`, systemd): runs with T-005 guest tests once Docker is there.

# T-166 – Copy rule: the firewall must report what a guest changed (host finding F2)

- **Owner:** pl-architect (implementer)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** –

## Goal
Found on the real host (T-158 step 4, F2): a port opened by hand in a guest (`tcp 8080`) gives the Issue `firewall:*` with only the attribute `table` "changed outside Plumb", without values. So the copy rule (D-331, D-350) never hashes the port. A `plan` token then proposed `tcp_ports 80,8080` on the same VM and on another VM; both Plans had no human-only reason and an `apply` token approved them. The copy rule must hold for `firewall` like for every other type. (The `authorized_keys` part of step 4 passed.)

## Context (read only these)
- `docs/tasks/T-158.log.md` step 4 on branch/worktree of pl-backend (ask him for the lines) · D-331, D-350, D-352, D-353 · review K3 of PR #248 (the `ssh` → `ssh_sources` mapping exists; ports and sources do not)
- `internal/resource/firewall*.go` (Observe / Diff), `internal/resource/guestvalue.go`, `internal/state/guestvalue.go`

## Acceptance criteria
- [ ] Say first, in the PR: which firewall attributes can a guest change by hand so that the report holds no value today (ports tcp/udp, sources, ssh_sources, policy, anything else). Check the other safe-apply type `network` and every resource with a derived/opaque observed attribute the same way; list the result per type.
- [ ] Fix so that every such value reaches the guest-value hashes: either the agent reports the observed rule set as list elements (ports, sources) or the server treats an opaque change of a type as "every list value of that type on any Target is guest data while the Issue is open and for the retention time". Choose the one that fails closed and say why; an old agent that still reports only `table` must also fail closed (agent version gate).
- [ ] Tests: the exact F2 case (hand port → `plan` token proposes it on the same and on another Target → `accept-guest-data`, `apply` token 403), UDP, a source address, an old agent. A general test: for every resource type, a hand change of each list attribute ends in a hash or in a fail-closed flag (so a new type cannot repeat this).
- [ ] No secret or full rule dump in the Issue text beyond what the type shows today for its own values.
- [ ] Decision numbers from `pl-lead` if needed. `make lint test` passes, CI guest test for the firewall case. PR to `main`, report.

## Out of scope
- Accept on `firewall:*` stays refused (T-125).

## Result (owner, max 8 lines)
branch · commit · tests · notes

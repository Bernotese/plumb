# T-103 – First apply of a unit together with its service is always refused

- **Owner:** pl-reviewer (as implementer)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** S–M   · **Depends on:** –   · **Parent:** T-089 (found in T-100, F2)

## Goal
On the real host the first apply of a `unit` plus a `service` for that unit was refused 2 of 2 times: the Plan was made from "absent", the Run sees "stopped" after the unit file exists, and `wire.Covers` says the Plan does not cover it. Every first `docker-compose` apply needed a second Plan. One approved Plan must be enough.

## Context (read only these)
- `docs/tasks/T-100.log.md` – finding F2
- `wire.Covers`, the `service` and `unit` resources, `blocks/docker-compose.yaml`

## Acceptance criteria
- [x] One Plan applies a new unit and starts its service; no refusal, no second Plan
- [x] The fix does not weaken the cover rule: a state change that the Plan did not show and that is not a direct result of an earlier step of the same Plan is still refused (say the rule in the PR; reviewer checks it)
- [x] Guest test on Debian 13 and Ubuntu 26.04: first apply of `docker-compose` with one Plan
- [x] `make lint test build` and `make test-guest` pass

## Out of scope
- Risk classes

## Plan (architect, optional)

## Result (owner, max 8 lines)
`pl-reviewer/T-103-unit-first-apply` · 17e44ae · `make lint test build` ok; `make test-guest` ok (Debian 13, Ubuntu 26.04)
- Rule (`resource.NewUnitCovers`, used only when `wire.Covers` fails): a planned `state: absent → S` also covers `state: stopped → S`, and `enabled: null → E` covers `enabled: false → E` (same to and risk), only when an earlier step that the service waits for (after, restart_on, earlier in its Block use) **created** the unit in this apply: `unit` of the same name, file `/etc/systemd/system/<unit>`, or a `package`. Everything else still needs `wire.Covers`.
- Still refused: unit made outside the apply, unrelated create, masked unit, other to or risk, unplanned restart, extra attributes (`internal/resource/newunit_test.go`, `internal/agent/newunit_test.go`; the agent test fails on main with the F2 output).
- Guest: `TestGuestStarterDockerCompose` first apply now uses a real observed Plan (not `unverified`).
- Not fixed (follow-up?): a service with `state: stopped, enabled: true` whose unit is absent at Plan time gets no planned entry, so its first enable is still refused.

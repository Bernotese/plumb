# T-103 – First apply of a unit together with its service is always refused

- **Owner:** pl-reviewer (as implementer)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** S–M   · **Depends on:** –   · **Parent:** T-089 (found in T-100, F2)

## Goal
On the real host the first apply of a `unit` plus a `service` for that unit was refused 2 of 2 times: the Plan was made from "absent", the Run sees "stopped" after the unit file exists, and `wire.Covers` says the Plan does not cover it. Every first `docker-compose` apply needed a second Plan. One approved Plan must be enough.

## Context (read only these)
- `docs/tasks/T-100.log.md` – finding F2
- `wire.Covers`, the `service` and `unit` resources, `blocks/docker-compose.yaml`

## Acceptance criteria
- [ ] One Plan applies a new unit and starts its service; no refusal, no second Plan
- [ ] The fix does not weaken the cover rule: a state change that the Plan did not show and that is not a direct result of an earlier step of the same Plan is still refused (say the rule in the PR; reviewer checks it)
- [ ] Guest test on Debian 13 and Ubuntu 26.04: first apply of `docker-compose` with one Plan
- [ ] `make lint test build` and `make test-guest` pass

## Out of scope
- Risk classes

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

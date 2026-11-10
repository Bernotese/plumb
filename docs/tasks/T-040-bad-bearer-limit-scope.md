# T-040 – Bad-bearer limit must not block valid tokens from the same IP

- **Owner:** tbd   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-030 on `main`   · **State:** backlog

## Goal
A client that retries a revoked or wrong bearer token cannot block other, valid tokens that come from the same IP address.

## Situation (from the T-030 review by pl-architect)
The bad-bearer limit (D-070) counts per client IP and runs before the token check. A client that retries a revoked token more than 20 times per minute makes every valid token from the same IP get 429 for up to 1 minute. Sessions are not affected, so there is no human lockout.

## Context (read only these)
- `docs/DECISIONS.md` D-070
- `internal/api` – the bearer limiter and its tests

## Acceptance criteria
- [ ] A valid token still works while another client on the same IP is limited for bad bearers.
- [ ] The limit still stops guessing of bearer values (no unbounded tries per IP).
- [ ] A new decision records the changed rule.
- [ ] Tests: `make lint test` passes.

## Out of scope
- Changes to the session login limit.

## Result (owner, max 8 lines)
branch · commit · tests · notes

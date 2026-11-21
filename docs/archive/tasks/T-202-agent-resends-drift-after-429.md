# T-202 – Agent: resend kept drift after the wait of a 429

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-180 merged

## Goal
After a rate-limited drift report (T-180) the agent keeps the items but sends them only at its next check (up to 15 min) or at a reconnect. D-366 says "after the wait". A timer at `Retry-After` (bounded, ≤ 2 min) flushes the kept items.

## Context (read only these)
- PR #287 (notes 1 and 2, review comment) · `internal/agent/drift.go`, `internal/agent/client.go`

## Acceptance criteria
- [ ] After a busy (429) drift send, one timer at `Retry-After` (capped at 2 min, with jitter) sends the kept items once; a second 429 arms it again; it never runs in parallel with a normal check and stops with the agent (name what ends each waiter).
- [ ] No tight loop: at most one resend per wait. Results and confirms are untouched.
- [ ] Amend the wording of D-366 in `docs/decisions/T-202.md` (the limit is applied after the strict decode; body cap 1 MiB) – no new number needed, write "D-366 · amended".
- [ ] Tests: real-agent test (the latest drift arrives ≤ Retry-After + a few seconds), shutdown during the wait. `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

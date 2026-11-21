# T-235 – `TestParkEndsOnLateUnbindResult` failed once under race

- **Owner:** pl-reviewer (author of T-210)   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
On the docs-only PR #353 the race job 132806860137 failed once: `internal/api` shard api1, `--- FAIL: TestParkEndsOnLateUnbindResult (1.88s)`; the interleaved output hides the assertion line (get the full log of the job; the shard ran 146 of 147 tests). The test covers D-396 / T-210: a late unbind result ends a park. A real race there would mean a guest that stays "agent left behind" for ever, or a park that ends for the wrong agent.

## Acceptance criteria
- [ ] Say with evidence whether it is a test fault (timing: a wait on wall time, the fake agent's order) or a product race (the engine mutex of T-210, the remembered late success, the store write). Reproduce it (a slowed step or `-race -count`), do not guess.
- [ ] Fix it without loosening the assertion; if it is a product race, tell `pl-lead` at once.
- [ ] The race job green 3 times in a row (rerun of the job or `-count` in CI); also make `scripts/test-race.sh` print the failing test's own output block un-interleaved (the assertion line was not findable in the log).
- [ ] `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

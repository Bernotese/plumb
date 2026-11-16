# T-251 – Find the cause of SQLITE_BUSY in TestAgentWithRealLink

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
`internal/agent` `TestAgentWithRealLink` failed once in CI (PR #376 at 14887f2, job "lint, test, build") with `database is locked (5) (SQLITE_BUSY)` at `link_integration_test.go:101`. The PR changed only web files. A rerun was green. Find the cause; a retry is not a fix. A busy store in a test may be a busy store in the product.

## Acceptance criteria
- [ ] Say who holds the lock at that line: how the test opens the store (`store.Open` settings: busy timeout, WAL, one writer), which goroutines write at that moment (the link's report handler, the drift limit path that logged "drift reports over the limit" just before), and whether the product can hit the same in `serve` (then it is a product bug: say so first, to `pl-lead`).
- [ ] Fix the cause (not a sleep, not a retry loop around the assert). A test that fails without the fix (a hook or a counter; run it `-count` many times).
- [ ] `make lint test`; PR to `main`; report with the proving CI job.

## Result (owner, max 8 lines)
branch · commit · tests · notes

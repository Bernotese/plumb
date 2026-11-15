# T-230 – `TestTokenRateHTTP` failed once under race

- **Owner:** pl-reviewer (author of T-179)   · **Reviewer:** pl-backend   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
On PR #340 (test-only for the restore, it does not touch the rate limit) the race job 132770722012 failed once: `internal/api` `--- FAIL: TestTokenRateHTTP (1.66s)`. The log holds no assertion line in the failed-log view; get the full log of the job.

## Acceptance criteria
- [ ] Say with evidence whether it is a test fault (timing of the bucket refill under a slow runner, a shared limiter between parallel tests) or a product fault (a token gets more or fewer calls than its bucket allows, or a session is limited).
- [ ] Fix it without loosening the assertion; a clock the test controls instead of real time if timing is the cause. Show the fix fails on the old code or with a mutant.
- [ ] `make lint test`; the race job green 3 times in a row (rerun of the job or `-count`); PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

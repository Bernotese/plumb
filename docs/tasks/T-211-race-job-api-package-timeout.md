# T-211 – Race job: `internal/api` runs into the 10 min package timeout

- **Owner:** pl-reviewer (was a Codex-size CI card; Codex is at its limit)   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
On main b26ccb2 the job "Go race detector" failed: `internal/api` "test timed out after 10m0s". pl-lead read the log: no hang (4 goroutines, the running test was 1 s old). Every package was 40–90 % slower than in the last green run (ctpack 124 → 236 s, issue 149 → 219 s, store 126 → 202 s), and `internal/api` was already at 406 s of 600 s. So: a slow runner plus a package too close to its limit.

## Acceptance criteria
- [ ] Measure: the 15 slowest tests of `internal/api` under `-race` (from a CI run with `-json` or `-v`), and why the slow ones are slow (real waits, sleeps, large fixtures, serial setup like `newEnv`/`backup.New`).
- [ ] Bring the package to ≤ ~50 % of its limit on a normal runner without dropping a test: make setup cheaper or shared where that is safe, replace sleeps by waits on a condition, `t.Parallel` where tests do not share state; and/or split the package's race run into two shards that run beside each other. A larger `-timeout` only as a guard on top.
- [ ] The job's own 15 min limit keeps a margin (state the new times). Every test still runs under `-race` (list in the PR how you checked: count of tests before = after).
- [ ] PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-216 – `TestDriftBusyResend` failed under race: test fault or an old value sent after a new one?

- **Owner:** pl-backend (author of T-180 / T-202)   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
On PR #316 (which does not touch the agent) the race job 132560966341 failed once: `internal/agent` `TestDriftBusyResend` (2.62 s), `drift_busy_test.go:142: drift v2: true {"type":"drift","items":[{"resource":"file:/srv/busy","status":"differs","changes":[{"attr":"content","from":"","to":"v0\n",…}]}]}`. The test wanted the value v2 and got a report with v0.

This matters beyond the test: the copy rule and the Issue match rely on "an older observation is never delivered after a newer one" (review of #312). T-202 added a resend path. If the resend can send a kept **old** item after a newer one, that is a product bug.

## Acceptance criteria
- [ ] Read the log and the code and say with evidence which it is: (a) the test reads the first report instead of waiting for the one with v2 (test fault), or (b) the agent really sent v0 after it had seen v2 (kept items of the busy path are sent as they were kept, not re-observed at send time).
- [ ] If (b): fix the agent so a resend always carries the state at send time (as the normal flush does), with a test that fails on the old code; tell `pl-lead` at once (the #312 claim then needs a note).
- [ ] If (a): fix the test without loosening it; show it fails when the agent is made to send the old value.
- [ ] Run the test 200× under `-race` in CI or say how you proved the fix where the dev VM has no gcc. `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

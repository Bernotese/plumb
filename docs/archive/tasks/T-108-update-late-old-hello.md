# T-108 – A late hello of the old agent process fails a good update (T-106 F3)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-102   · **Parent:** T-089 (slice 3 fix)

## Goal
An agent update whose link drops for a short time (about 30 s) must not end as "the agent came back with its old binary" when the swap worked.

## Context (read only these)
- `docs/tasks/T-106.log.md` – F3 with the timeline and the fix idea
- `internal/plan/agentupdate.go` (around line 633) and its tests; D-284, D-285 in `docs/DECISIONS.md`
- `docs/ARCHITECTURE.md` §14

## Acceptance criteria
- [ ] A hello with the old sha from the old process (after its `applied` result, before it exits) does not fail the Run; the Run ends `ok` on the hello of the new binary
- [ ] A real revert still fails the Run: a hello with `reverted`, or the old binary after a real restart, within the existing waits. Say in the PR how the two cases are told apart, and why a stale or forged hello cannot turn a failed update into `ok`
- [ ] A test replays the F3 timeline (result received, reply lost, reconnect hello with the old sha, resend → 409, new hello); the T-102 tests stay green
- [ ] Wire change only if there is no other way; then ask pl-lead first (contract T-090)
- [ ] New decisions: `docs/decisions/T-108.md`, numbers D-297, D-298
- [ ] `make lint test build` green; small diff

## Out of scope
- The 5 min guard limit (known, documented); a new host run (pl-lead decides after the merge)

## Result (owner, max 8 lines)
branch · commit · tests · notes

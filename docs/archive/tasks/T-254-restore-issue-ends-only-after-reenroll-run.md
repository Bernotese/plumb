# T-254 – The restore Issue ends only when the re-enroll Run has worked (host finding F4, T-247)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer (critical: restore path, the D-439 gate)   · **QA:** the new tests + T-247 on the host
- **Size:** S   · **Depends on:** T-252 (merged)

## Goal
Found on the real host (T-247 step 9, after T-252): two Targets were restored; each got its re-enroll Plan and its restore Issue ended at that moment ("approve the re-enroll Plan N"). Approving the first Plan moved the head, so the second Plan went stale (409 plan-stale). The second guest is stopped, its agent credential is revoked, no Issue is open and no Plan is pending: a silent state again.

## Decision D-440 (pl-lead; write it to `docs/decisions/T-254.md`)
After a restore, `guest:restore-failed` stays open in the D-439 "restored" state until the re-enroll **Run** of that Target has worked. It never ends when the Plan is made. A stale, rejected, expired or failed re-enroll leaves the Issue open with the way out (plan the re-enroll again). This makes D-439 the one rule for "restored, not enrolled yet", with or without a refusal.

## Acceptance criteria
- [ ] The Issue ends only by `endRestoredByReenroll` (the Run worked), by a new restore, or by the Target delete. Its text in the normal case: "restored; the agent is not enrolled yet: approve the re-enroll Plan N" and, when that Plan can no longer run (stale, rejected, expired, failed Run), "plan the re-enroll again" with the action – the text follows the Plan's real state at read time, not the state at write time.
- [ ] The D-439 gate is unchanged (only that Target's re-enroll Plan passes, proof from the store); the web shows the state and the action; `explain_issue` gives the same way out.
- [ ] Not in this card: a re-enroll Plan that survives a head move (say in the PR whether that is easy and safe; if yes, a proposal for a later card).
- [ ] Tests: two Targets restored, the first re-enroll approved → the second Plan stale → its Issue is open and offers the re-enroll; a new re-enroll Plan → Run works → the Issue ends with the system comment; rejected / failed Run paths; the QA tests that pin "the Issue ends at the Plan" change to the new rule (list them for pl-reviewer). Mutants.
- [ ] `make lint test`; PR to `main`; report. Host proof: T-247 step 9 once more.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-252 – The re-enroll dedupe counts Plans that ran long ago (host finding, T-247 step 9)

- **Owner:** pl-backend (author of #374)   · **Reviewer:** pl-reviewer (critical: restore path)   · **QA:** the new tests + T-247 on the host
- **Size:** S   · **Depends on:** –

## Goal
Found on the real host: `openReenroll` (the "one re-enroll Plan" dedupe of T-241, host D12) counts APPROVED re-enroll Plans, and a Plan stays "approved" after its Run has finished. After a second restore of a Target that was re-enrolled before, Plumb makes no re-enroll Plan and ends `guest:restore-failed` with "the re-enroll Plan N is still open … approve it" – but Plan N ran long ago. The guest is stopped, its agent credential revoked, no Issue is open: a silent state. No test approved the first re-enroll, so CI and QA passed.

## Acceptance criteria
- [ ] Only a re-enroll Plan that can still do the work counts: pending, or approved with no Run yet or a Run that has not ended. A Plan whose Run ended (ok or failed), a rejected, expired or superseded Plan does not count.
- [ ] The text of the Issue end never names a Plan that cannot be approved or is done. If Plumb makes no re-enroll Plan for any reason, the Issue stays open in the D-439 state (never a silent end with a stopped, revoked guest).
- [ ] Tests: restore → approve its re-enroll → Run ends → second restore → a NEW re-enroll Plan; the same with a failed re-enroll Run; with a rejected Plan; a pending Plan still dedupes (one Plan, not two). Mutants. Say which other "is there an open Plan" checks in the code have the same fault (grep for the pattern: status approved without the Run state) and fix or list them.
- [ ] `make lint test`; PR to `main`; report. The host proof is T-247 step 9 again.

## Result (owner, max 8 lines)
branch · commit · tests · notes

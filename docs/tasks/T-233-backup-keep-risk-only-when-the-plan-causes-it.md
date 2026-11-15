# T-233 – Backup field: a Plan is destructive only when it causes more deletes than the old field (host observation S1)

- **Owner:** pl-architect (author of the rule in #334)   · **Reviewer:** pl-reviewer (critical: it must keep M1 of #334 closed)   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
Seen on the real host (T-225 part 1, S1): a Plan that only moves the backup time (`keep` unchanged, at the keep limit) is `destructive` with "the next backup job deletes 1 backup(s)". The old field deletes the same backup at its next slot, so the Plan does not cause that delete. Every schedule change at the keep limit then needs a person.

## Acceptance criteria
- [ ] Rule (proposal of pl-reviewer): destructive iff `NextJobDeletes(new) > NextJobDeletes(old)`; an absent old field counts as 0 (so "remove the field, then add it again with keep 1" stays destructive: M1 of #334). The Plan text says how many more backups go than with the old value. An unknown count stays fail closed as today.
- [ ] Tests: time-only change at the limit = read; lower keep = destructive with the difference; remove → re-add with a token = destructive, 403; unknown count cases unchanged. Mutants for the comparison.
- [ ] `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

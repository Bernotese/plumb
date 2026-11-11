# T-065 – Image builds: QA

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M (one QA PR per card)   · **Depends on:** each card T-058 … T-064   · **Parent:** T-039

## Goal
Independent tests prove each image build card against the plan.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §8; the card under test and its PR

## Acceptance criteria
- [ ] Per card: tests for its acceptance criteria that the author did not write; a mutation audit of the guards.
- [ ] Review items each have a failing-if-removed test: B1 (no conversion before `stopped`; forced stop fails the build), S1 (Block cannot forge the build mark), S2 (stale build Plan), S3 (linked clone blocks delete), S4 (revoke + delete on every exit path, crash in each step then `Recover`), S5 (two lanes cannot both pass the 85 % check), S6 (no symlink follow), n1 … n3.
- [ ] Guest tests (T-060) run in CI for both distributions.
- [ ] Tests: `make test` passes; findings go to pl-lead.

## Out of scope
- The real host (T-066).

## Result (owner, max 8 lines)
branch · commit · tests · notes

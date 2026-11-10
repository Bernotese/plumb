# T-026 – Port the lost QA crash test to main

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** –

## Goal
The independent QA crash test from PR #16 is on `main`. PR #16 can be closed.

## Situation
T-010 was squash-merged before the last QA commit landed. `d8266be` on `origin/pl-codex-qa/T-010-qa` adds
`TestCrashDuringResolveRunRecoversIssue`. `main` does not have it. `main` only has the author's `TestRecoverAfterCrash`.

## Context (read only these)
- `git show d8266be` – the test and the log lines
- `internal/issue/recovery_qa_test.go` on `main` – target file
- `internal/issue/review_test.go` – the author's test, for the current helper names

## Acceptance criteria
- [ ] New branch from `main`. `TestCrashDuringResolveRunRecoversIssue` is in `internal/issue/recovery_qa_test.go`.
- [ ] Keep the `main` version of `TestConcurrentApprovalAppliesOnce` (it accepts `plan.ErrTargetBusy`). Do not revert it.
- [ ] Add the two missing log lines to `docs/tasks/T-010.qa.log.md`.
- [ ] No production code changes. If the test fails on `main`, stop and report the failure. Do not change the engine.
- [ ] Tests: `make lint test` passes. Run the new test 20 times: `go test ./internal/issue -run TestCrashDuringResolveRunRecoversIssue -count=20`.
- [ ] PR against `main`. Diff ≤ ~70 lines.

## Out of scope
- New test ideas. Report them in one line instead.

## Result (owner, max 8 lines)
branch · commit · tests · notes

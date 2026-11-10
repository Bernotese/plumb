# T-007 – Guest test harness

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** T-003   · **Parallel with:** T-004, T-005, T-008
- **Status:** in review; guest run pending Docker access

## Goal
The team can run resource tests inside real Debian and Ubuntu systems with one command.

## Context (read only these)
- `docs/ARCHITECTURE.md` §8
- `Makefile` (from T-003)
- Files to add: `test/guest/Dockerfile`, `test/guest/run.sh`, one sample test with build tag `guest`

## Acceptance criteria
- [ ] `make test-guest` builds the test binary on the host and runs it in a `debian:13` and an `ubuntu:26.04` container with systemd as PID 1.
- [ ] The command fails when a test fails. It removes its containers in every case.
- [ ] The sample test installs a small package and starts a service. It passes on both systems.
- [x] A CI job runs `make test-guest`.
- [x] Diff ≤ ~150 lines.

## Out of scope
- Tests for the real resources (they come with T-005).

## Plan (architect, optional)

## Result (owner, max 8 lines)
`pl-codex-qa/T-007-guest-test-harness` · see PR commit
Tests: `go test ./...`, `make lint-go`, `bash -n test/guest/run.sh`, `git diff --check` pass.
`make test-guest` compiles the guest binary, then fails on Docker socket access.
Guest runs on both distributions, test failure propagation, and container cleanup remain pending.
Docker access needs user action; no permission workaround attempted.

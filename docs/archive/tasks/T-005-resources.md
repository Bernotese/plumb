# T-005 – First resources

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-003; T-007 for the guest tests   · **Parallel with:** T-004, T-008, T-009
- **Status:** review (guest run pending: Docker access, T-007)

## Goal
Five resource types can observe, diff and apply inside a Debian or Ubuntu guest.

## Context (read only these)
- `docs/ARCHITECTURE.md` §2, §3, §6, §7
- `internal/wire`, `internal/resource` (from T-003)
- `test/guest` (from T-007)

## Acceptance criteria
- [ ] `file`, `directory`, `package`, `service`, `user` implement `Resource`. A second apply reports no change.
- [ ] Every `Diff` sets a risk per change. `package` has strictness `present` (default) and `exact`.
- [ ] `service` handles the `restart` change. It restarts once, also when several `restart_on` resources changed.
- [ ] All package-manager operations of one guest share one lock. A test applies two `package` resources at the same time.
- [ ] For an attribute listed in `sensitive`, the resource compares locally and returns only a change with `sensitive: true` (no `from`, no `to`). A test proves that no report holds the clear value or a hash of it. A resource that is not on the guest is observed as `state: absent`. Every command gets `--` before its operands.
- [ ] The root directory and the command runner are injected. Unit tests use a temp directory and a fake runner. No test needs root.
- [ ] Tests: `go test ./internal/resource/...` passes. `make test-guest` passes on Debian 13 and Ubuntu 26.04.
- [ ] From the T-003 re-review: `Item.Output` is not covered by `wire.Redact`. Add one rule in `resource.go` (no sensitive value in Output) and a test.

## Out of scope
- The agent loop and drift (T-018).
- Other resource types, the `command` resource.

## Plan (architect, optional)

## Result (owner, max 8 lines)
- Branch `pl-frontend/T-005-resources`, PR #8. Five types in `internal/resource/{file,directory,package,service,user}.go`; shared code in `env.go`, `fsx.go`. Rules: D-054 (architect text).
- Review fixes: no symlink in any path component (walk with `openat(O_NOFOLLOW)`, then fds only; hard links refused); minimal command env; `AccountLock`; `SafeOutput` hides all output of a sensitive resource (QA finding: old value); package name not ending in `-`; uid 0 guard; masked units; user updates that can take access away are `disruptive`.
- Locks: `PackageLock` (dpkg/apt), `AccountLock` (shadow tools). `RestartChange` + one restart per apply.
- Tests: `go test ./internal/resource/...` pass incl. QA branch (merged); symlink/hard-link/path tests; mutation checks for `--` and `O_NOFOLLOW`.
- Guest tests (tag `guest`, incl. a root symlink test) compile; run **pending** (Docker, T-007).

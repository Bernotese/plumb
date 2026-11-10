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
- Branch `pl-frontend/T-005-resources`. Five types in `internal/resource/{file,directory,package,service,user}.go`; shared code in `env.go` (`NewEnv`, `ExecRunner`, passwd/group parsing). Rules: D-054.
- `Env.PackageLock` serializes every dpkg/apt call of a guest; apt waits for the dpkg lock (`DPkg::Lock::Timeout`), runs `apt-get update` once when an install fails.
- Restart: `RestartChange(svc, changed)` gives one change however many `restart_on` resources changed; `service.Apply` restarts at most once, not after a start, not when it must stay stopped.
- Sensitive: `Diff` compares locally, then `wire.Redact`; `Apply` takes values from the attributes, never from `To`. `SafeOutput` in `resource.go` redacts values and their lines in `Item.Output`.
- Tests: `go test ./internal/resource/...` → pass (fake guest runner; fails any call without `--` before its operand; mutation checked). Coverage 84 %.
- Guest tests: `test/guest/resource_test.go` (tag `guest`) compiles; run **pending** (no Docker access; T-007 harness not merged). LXD run was denied.

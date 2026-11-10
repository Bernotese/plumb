# T-005 – First resources

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-003; T-007 for the guest tests   · **Parallel with:** T-004, T-008, T-009
- **Status:** proposed (do not start before the user approves the architecture)

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
- [ ] For an attribute listed in `sensitive`, the resource compares locally and returns only `changed: true/false`. A test proves that no report holds the clear value or a hash of it.
- [ ] The root directory and the command runner are injected. Unit tests use a temp directory and a fake runner. No test needs root.
- [ ] Tests: `go test ./internal/resource/...` passes. `make test-guest` passes on Debian 13 and Ubuntu 26.04.

## Out of scope
- The agent loop and drift (T-018).
- Other resource types, the `command` resource.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

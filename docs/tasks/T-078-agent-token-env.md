# T-078 – agent: `token_env`, `symlink` EINVAL text

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-075
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
The agent in a container reads its enrollment token from the environment of PID 1, and `symlink` fails with a clear text without `renameat2` flags.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §5 (D-228, D-231)
- `internal/agent`

## Acceptance criteria
- [ ] Everything in §5 of the plan except `seal` (T-082); the comment at `sealfs.go` is fixed.
- [ ] Tests: `make lint test build` and `sg docker -c "make test-guest"` pass.
- [ ] New decisions go to `docs/decisions/T-078.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- `seal` in a container.

## Result (owner, max 8 lines)
branch · commit · tests · notes

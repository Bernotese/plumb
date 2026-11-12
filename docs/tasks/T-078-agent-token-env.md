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
`pl-frontend/T-078-agent-token-env` · `make lint test build` → ok; `sg docker -c "make test-guest"` → ok (Debian 13, Ubuntu 26.04).
- `agent.json` `token_env`: the agent reads `/proc/1/environ` (≤ 64 KiB, NUL list, first entry wins, missing/empty = "no token yet", retry); errors never hold a value. `token_env` and `token_file` exclude each other; a `token_env` agent does not write the cloud-init flag (no cloud-init in a CT, host D5).
- `symlink`: `renameat2` EINVAL → "the file system of <dir> does not support atomic rename …; on ZFS it needs ZFS 2.2 or newer", no fallback (D-228); seam `renameat2` + test.
- `wire.IsAgentPath` and the `service` check cover `plumb-agent-install.service` (unit, drop-ins, wants link). `sealfs.go` comment: Proxmox rewrites the files at every start, no bind mount (host D1).
- No new decision: all inside D-228/D-231.

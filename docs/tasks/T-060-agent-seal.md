# T-060 – Image builds: agent command seal

- **Owner:** pl-frontend (was pl-backend, second lane)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-065)
- **Size:** L   · **Depends on:** T-058   · **Parent:** T-039

## Goal
The agent in a build guest makes the guest clean for a template, and it can never do that in a real Target.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §4 (all), §8 rows "Agent unit" and "Guest, in CI"; host log C0 … C7, D2 … D5; `docs/decisions/T-039.md` D-205 … D-207
- `cmd/plumb-agent`, `internal/agent` (credential file, enrollment, resources), `internal/link` (`Send`)

## Acceptance criteria
- [x] Steps 1 … 11 of Plan §4 in this order, fixed in the agent; `dracut` step only when the command exists; upgrade check ignores phased packages; `userdel -r -f`.
- [x] Review S1: build mark and `watch` mark in the credential file, same atomic write; build mark written only at enrollment, cleared by a normal enrollment; `watch` mark never taken back (also after a restart). The agent refuses `file`/`directory`/`symlink` on its own paths (agent side of T-059). Test: a Block that tries to write the mark → `seal` still `refused`.
- [x] Review of T-059 (S1, N1): the save side (`wire.AgentConflicts`) knows only the links of the same resource list, the aliases `/var/run`, `/bin`, `/sbin`, `/lib` and the systemd unit dirs. The agent must check the **real** path before it writes, links or removes: resolve every existing symlink in the guest (`openat2` with `RESOLVE_NO_SYMLINKS`/`RESOLVE_BENEATH`, or resolve and compare with `wire.IsAgentPath`), and refuse when the result is an agent path. Also refuse a `service` resource on `plumb-agent`. Tests: a link `/opt/x → /var/lib` that exists in the guest only, then `file /opt/x/plumb-agent/credential` → `refused`; the same with a link of the unit dir.
- [x] Review of T-058 (S1): the agent takes `build=true` only at a first enrollment from an empty state (no credential, never a `watch`); a re-enrollment with `build=true` leaves the mark unset; no enrollment clears the `watch` mark. Tests for all three.
- [x] Guards: no build mark or a `watch` seen → `refused`, nothing changed. Controller side: `link.Send` takes `seal` only for a link name with prefix `build:` (unit test). T-062 part B adds: no `watch` to a `build:` link, `EnrollResponse.Build` for a `build:` token, and the engine that takes the seal result.
- [x] Review B1: order result → controller takes it → remove credential, desired list, unit, config, CA, install script, binary → `sync` → `systemctl poweroff`. The agent sends no second result.
- [x] Review S6: the `/var/log` walk uses `lstat`, opens with `O_NOFOLLOW`, stays on one file system, never writes through a symbolic link (test: link to `/etc/shadow`).
- [x] Guest tests in CI: Debian 13 and Ubuntu 26.04 containers with `cloud-init` and `openssh-server`: after `seal` no host key, machine-id reset, no cloud-init state, every file under `/var/log` empty, no `plumb-build`, no agent file or unit; a normal agent refuses; the S6 case; the power-off is called. Initramfs step: fake runner only.
- [x] Docs (n4): ARCHITECTURE §7 names that the controller cannot prove that `seal` ran.
- [x] Tests: `make test`, guest tests job green on the PR.

## Out of scope
- When the engine sends `seal` and the wait for `stopped` (T-062). LXC templates (T-038).

## Result (owner, max 8 lines)
- Branch `pl-frontend/T-060-agent-seal`. Agent: `internal/agent/seal.go` (steps), `sealfs.go` (no-follow file work, S6), `ownpath.go` (real path through guest links), `store.go` (credential file = JSON `{credential, build, watch, enrolled}`; an old plain-text file reads as enrolled, no build mark). Link: `Send` takes `seal` only for `build:<name>` with a `SealBody`; a seal result holds only `seal:<step>` items (each once, no changes, no progress); no other command or drift may hold one.
- Handshake: the agent removes its files only after the controller took the result (204), then `sync`, then `systemctl poweroff`; it never polls again. When a file cannot be removed it does not power off (the controller times out and deletes the guest).
- Choices: `fstrim` failing (no discard) is `skipped`, not `failed`. `build-user` fails when the home of `plumb-build` is not `/home/plumb-build` (userdel -r would remove another tree). The upgrade check reads the old (`N upgraded, N newly installed`) and the apt 3 (`Upgrading: N, Installing: N`) summary and counts `Inst` lines; phased packages do not count.
- Not here (T-062): `EnrollResponse.Build` from a token marked `build`; the engine's handling of a seal result (`OnReport`, `Sensitive` lookup for a `build:` link).
- Tests: `make lint test build` ok; `sg docker -c "make test-guest"` ok on Debian 13 + Ubuntu 26.04 (the guest image now has cloud-init, openssh-server, rsyslog, procps). One earlier run had a flaky `TestGuestAgentLoop` apt canary timeout (30 s) on Ubuntu; the rerun was green.

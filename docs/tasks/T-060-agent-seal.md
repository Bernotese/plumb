# T-060 – Image builds: agent command seal

- **Owner:** pl-backend (second lane)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-065)
- **Size:** L   · **Depends on:** T-058   · **Parent:** T-039

## Goal
The agent in a build guest makes the guest clean for a template, and it can never do that in a real Target.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §4 (all), §8 rows "Agent unit" and "Guest, in CI"; host log C0 … C7, D2 … D5; `docs/decisions/T-039.md` D-205 … D-207
- `cmd/plumb-agent`, `internal/agent` (credential file, enrollment, resources), `internal/link` (`Send`)

## Acceptance criteria
- [ ] Steps 1 … 11 of Plan §4 in this order, fixed in the agent; `dracut` step only when the command exists; upgrade check ignores phased packages; `userdel -r -f`.
- [ ] Review S1: build mark and `watch` mark in the credential file, same atomic write; build mark written only at enrollment, cleared by a normal enrollment; `watch` mark never taken back (also after a restart). The agent refuses `file`/`directory`/`symlink` on its own paths (agent side of T-059). Test: a Block that tries to write the mark → `seal` still `refused`.
- [ ] Review of T-059 (S1, N1): the save side (`wire.AgentConflicts`) knows only the links of the same resource list, the aliases `/var/run`, `/bin`, `/sbin`, `/lib` and the systemd unit dirs. The agent must check the **real** path before it writes, links or removes: resolve every existing symlink in the guest (`openat2` with `RESOLVE_NO_SYMLINKS`/`RESOLVE_BENEATH`, or resolve and compare with `wire.IsAgentPath`), and refuse when the result is an agent path. Also refuse a `service` resource on `plumb-agent`. Tests: a link `/opt/x → /var/lib` that exists in the guest only, then `file /opt/x/plumb-agent/credential` → `refused`; the same with a link of the unit dir.
- [ ] Review of T-058 (S1): the agent takes `build=true` only at a first enrollment from an empty state (no credential, never a `watch`); a re-enrollment with `build=true` leaves the mark unset; no enrollment clears the `watch` mark. Tests for all three.
- [ ] Guards: no build mark or a `watch` seen → `refused`, nothing changed. Controller side is done in T-062 part B: `link.Send` takes `seal` only for a `build:` link and sends no `watch` to one, link takes `seal:` items only in a result of a build link, and `EnrollResponse.Build` is true for a `build:` token. The agent answers in the form of `wire.SealSteps` / `SealItemID` (see `linktest` `seal` for the expected behaviour).
- [ ] Review B1: order result → controller takes it → remove credential, desired list, unit, config, CA, install script, binary → `sync` → `systemctl poweroff`. The agent sends no second result.
- [ ] Review S6: the `/var/log` walk uses `lstat`, opens with `O_NOFOLLOW`, stays on one file system, never writes through a symbolic link (test: link to `/etc/shadow`).
- [ ] Guest tests in CI: Debian 13 and Ubuntu 26.04 containers with `cloud-init` and `openssh-server`: after `seal` no host key, machine-id reset, no cloud-init state, every file under `/var/log` empty, no `plumb-build`, no agent file or unit; a normal agent refuses; the S6 case; the power-off is called. Initramfs step: fake runner only.
- [ ] Docs (n4): ARCHITECTURE §7 names that the controller cannot prove that `seal` ran.
- [ ] Tests: `make test`, guest tests job green on the PR.

## Out of scope
- When the engine sends `seal` and the wait for `stopped` (T-062). LXC templates (T-038).

## Result (owner, max 8 lines)
branch · commit · tests · notes

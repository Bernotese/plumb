# T-104 – docker-engine Block also for LXC Targets

- **Owner:** pl-frontend   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** –   · **Parent:** T-089 (T-100 result for Question 4)

## Goal
T-100 showed: Docker runs in an unprivileged LXC with `nesting=1` only, without `keyctl` and without new rights. D-262 says `lxc` is allowed in that case. Today only the `sysctl` in `docker-engine` blocks an LXC Target (dockerd sets `ip_forward` in the container's own network namespace).

## Context (read only these)
- `docs/tasks/T-100.log.md` – step 5 (the test Block without the sysctl)
- `blocks/docker-engine.yaml`, `blocks/docker-compose.yaml`, D-262, D-280

## Acceptance criteria
- [x] `docker-engine` works on VM and LXC Targets with one Block (pl-lead's proposal: drop the `sysctl`, dockerd sets forwarding itself; show in the VM guest test that forwarding still works; if that does not hold, propose another form)
- [x] Descriptions updated (no "VM only"); the firewall note stays
- [x] Guest tests on Debian 13 and Ubuntu 26.04; say what the CI can and cannot show for LXC (the host proof is in T-100)
- [x] Decision line with a number from pl-lead; `make lint test build` and `make test-guest` pass

## Out of scope
- `keyctl`, privileged containers, new token rights

## Plan (architect, optional)

## Result (owner, max 8 lines)
- Branch `pl-frontend/T-104-docker-engine-lxc`. `docker-engine` drops the `sysctl`; the description says VM and LXC (LXC needs only `nesting=1`, which Plumb sets); the firewall note stays. D-283 in `docs/decisions/T-104.md`.
- Unit (`blocks/starter3_test.go`): all five starter Blocks + compose render on `lxc` and `vm`, no `sysctl:` id.
- Guest (`TestGuestStarterDockerCompose`, Debian 13 + Ubuntu 26.04): starts with `ip_forward=0`; after the apply it is `1` (set by dockerd), no `/etc/sysctl.d` file, and a container pings the guest's gateway (routed docker0 → eth0). Negative check by hand in a dind container: with `ip_forward=0` the same ping fails (rc 1), with `1` it passes.
- CI limit: the guest is a privileged Docker container with its own network namespace, not an unprivileged LXC. CI shows that dockerd sets forwarding itself and that forwarding works; it cannot show the LXC limits (read-only `/proc/sys`, no `keyctl`). That proof is T-100 step 5.
- Note: VM Targets that applied the old Block keep `/etc/sysctl.d/60-plumb-net.ipv4.ip_forward.conf` (value 1, harmless).

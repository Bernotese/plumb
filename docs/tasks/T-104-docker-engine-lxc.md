# T-104 – docker-engine Block also for LXC Targets

- **Owner:** pl-frontend   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** –   · **Parent:** T-089 (T-100 result for Question 4)

## Goal
T-100 showed: Docker runs in an unprivileged LXC with `nesting=1` only, without `keyctl` and without new rights. D-262 says `lxc` is allowed in that case. Today only the `sysctl` in `docker-engine` blocks an LXC Target (dockerd sets `ip_forward` in the container's own network namespace).

## Context (read only these)
- `docs/tasks/T-100.log.md` – step 5 (the test Block without the sysctl)
- `blocks/docker-engine.yaml`, `blocks/docker-compose.yaml`, D-262, D-280

## Acceptance criteria
- [ ] `docker-engine` works on VM and LXC Targets with one Block (pl-lead's proposal: drop the `sysctl`, dockerd sets forwarding itself; show in the VM guest test that forwarding still works; if that does not hold, propose another form)
- [ ] Descriptions updated (no "VM only"); the firewall note stays
- [ ] Guest tests on Debian 13 and Ubuntu 26.04; say what the CI can and cannot show for LXC (the host proof is in T-100)
- [ ] Decision line with a number from pl-lead; `make lint test build` and `make test-guest` pass

## Out of scope
- `keyctl`, privileged containers, new token rights

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-012 – Proxmox driver for VMs

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (Claude)   · **Depends on:** T-003; real-host checks need the test host   · **Parallel with:** T-004 … T-009
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
Plumb can import a cloud image, create a VM from it with the agent bootstrap, change it, observe it and delete it on a real Proxmox host.

## Context (read only these)
- `docs/ARCHITECTURE.md` §2, §5, §7, §8
- `internal/driver` (from T-003)
- `docs/tasks/T-001-architecture.md` → Plan → "Facts to verify"

## Acceptance criteria
- [ ] Thin REST client with the standard library: token header, pinned TLS fingerprint, wait for async tasks, typed errors.
- [ ] `FetchImage`: `download-url` with SHA-256, create a VM with `import-from`, add the cloud-init drive, convert to a template.
- [ ] `Create`: clone into pool `plumb`, set CPU, memory, disk, network and cloud-init fields, write the vendor-data snippet with the enrollment data, start.
- [ ] The snippet file has mode 0600 where the storage allows it. The driver deletes it after enrollment or after 15 minutes.
- [ ] `Observe`: config, config digest, power state, IP addresses. `Update`: cores, memory, disk grow. `Power`. `Delete`.
- [ ] Every change has a risk: memory or cores without hotplug = `disruptive`, delete = `destructive`.
- [ ] The driver touches only guests in its pool. It refuses any other VMID.
- [ ] Tests: `go test ./internal/driver/proxmox/...` passes with recorded answers in `testdata/pve` (no token inside). `make test-pve` passes on the test host and leaves no guest behind.

## Out of scope
- LXC, image builds with Blocks, snapshots, backups.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-012 – Proxmox driver for VMs

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (Claude)   · **Depends on:** T-003, T-013; needs the test host   · **Parallel with:** host track: after T-013
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
Plumb can import a cloud image, create a VM from it with the agent bootstrap, change it, observe it and delete it on a real Proxmox host.

## Context (read only these)
- `docs/ARCHITECTURE.md` §2, §5, §7, §8
- `internal/driver` (from T-003)
- `docs/tasks/T-001-architecture.md` → Plan → "Facts to verify"

## Acceptance criteria
- [ ] Thin REST client with the standard library: token header, wait for async tasks, typed errors. TLS trusts a CA file (the host `pve-root-ca.pem`) or the system roots. It never skips verification.
- [ ] `FetchImage`: `download-url` with the SHA-256 that is pinned in the Image document, create a VM with `import-from`, add the cloud-init drive, convert to a template.
- [ ] `Create`: clone into pool `plumb`, set CPU, memory, disk, network and cloud-init fields, set `cicustom vendor=` to the static snippet, set the token as SMBIOS serial, start. The driver writes no file on the host.
- [ ] Re-enroll: `Update` sets a new serial; the guest needs a stop and a start.
- [ ] `Observe`: config, config digest, power state, IP addresses. `Update`: cores, memory, disk grow. `Power`. `Delete`.
- [ ] `Diff` is pure and sets the risk: memory or cores without hotplug = `disruptive`, disk shrink or delete = `destructive`.
- [ ] The driver touches only guests in its pool. It refuses any other VMID.
- [ ] Real-host checks: a reboot after enrollment works; a later change of a cloud-init field does not create new SSH host keys and no new first-boot run.
- [ ] Tests: `go test ./internal/driver/proxmox/...` passes with recorded answers in `testdata/pve` (no token inside). `make test-pve` passes on the test host and leaves no guest behind.

## Out of scope
- LXC, image builds with Blocks, signed checksums, snapshots, backups.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

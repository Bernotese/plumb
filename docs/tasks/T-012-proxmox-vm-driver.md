# T-012 – Proxmox driver for VMs

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (Claude)   · **Depends on:** T-003, T-013; needs the test host   · **Parallel with:** host track: after T-013
- **Status:** proposed · **blocked** until `pl-lead` confirms the Proxmox test host (D-041)

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
- [ ] Re-enroll: `Update` with a `Bootstrap` sets a new serial and keeps the other `smbios1` fields (the `uuid` must not change); the guest needs a stop and a start.
- [ ] `Observe`: config, config digest, power state, IP addresses. `Update`: cores, memory, bridge. `Power`. `Delete`.
- [ ] `Diff` is pure and sets the risk: memory or cores without hotplug = `disruptive`, delete = `destructive`. It marks `ip`, bootstrap user and `disk` as create-only: a change of them is an error, not a change.
- [ ] The driver touches only guests in its pool. It refuses any other VMID.
- [ ] Real-host checks: a reboot after enrollment works; Plumb never changes a cloud-init field (D-185); a change from outside keeps the SSH host keys (`ssh_deletekeys: false` in the vendor snippet, PR #26). Not tested: a bridge change on a real guest (the test host has one bridge; T-053/D-184 allows only `PLUMB_PVE_BRIDGE`). Test it on a host with two bridges before more than one bridge is allowed.
- [ ] Tests: `go test ./internal/driver/proxmox/...` passes with recorded answers in `testdata/pve` (no token inside). `make test-pve` passes on the test host and leaves no guest behind.

## Out of scope
- LXC, image builds with Blocks, signed checksums, snapshots, backups.

## Plan (architect, optional)

## To verify on host (offline part written against the PVE API schema, not recordings)
Each item is an assumption of `internal/driver/proxmox` and of its fake server (`pvefake_test.go`). Check it on PVE ≥ 9.1 with the restricted token, then replace the fake answer by a recorded one in `testdata/pve`. Risk marks from the static review: verify **[high]** first, then **[medium]**; the rest is low.
1. Header `Authorization: PVEAPIToken=plumb@pve!<name>=<secret>` works with privilege separation.
2. **[high]** `GET /pools?poolid=plumb&type=qemu` returns `[{members:[{vmid,type,name,template}]}]`. **Rights:** does the token need `Pool.Audit` on `/pool/plumb` to list members? D-032's role has no `Pool.Audit`; if it is needed, D-032 and the installer change.
3. **[medium]** `GET /cluster/nextid?vmid=N` answers 400 for a taken VMID, also for a VMID that the token cannot see, and needs no extra right.
4. **[high]** `download-url` with `content=import`, `checksum`, `checksum-algorithm=sha256`, `verify-certificates=1` works with `Sys.AccessNetwork` + `Datastore.AllocateTemplate`; PVE refuses a checksum mismatch as a failed task; the filename must end in `.qcow2` for `import` (Ubuntu's `.img` is qcow2).
5. **[medium]** `POST /nodes/{n}/qemu` with `scsi0=<disk>:0,import-from=<images>:import/<file>` works with the token (rights on the image storage for `import-from`).
6. `POST …/template` answers synchronously (null) or with a UPID (both handled).
7. `POST …/clone` with `full=1`, `pool`, `storage` makes a full clone in the pool, and the clone gets a new `smbios1` uuid.
8. **[medium]** `POST …/config` answers with a UPID or null (both handled); a stale `digest` is refused with "detected modified configuration".
9. `sshkeys` must be URL-encoded with `%20` for spaces (and then form-encoded).
10. **[high]** `smbios1` with `base64=1`: the serial and the other string fields are base64, the uuid is not; length limit of the serial (token 43 chars); the guest sees the decoded value in `/sys/class/dmi/id/product_serial`.
11. **[high]** `cicustom vendor=plumb-snippets:snippets/<file>` is accepted with only `Datastore.Audit` on the snippet storage (fact list of T-001); the token cannot write snippets.
12. `PUT …/resize` takes an absolute size (`20G`) and answers with a UPID.
13. `GET …/config?current=1` gives `memory` as a number or as `current=N` (both parsed); `cores` may be missing (default 1).
14. **[medium]** A missing VM answers 500 "does not exist"; a VM outside the token's rights may answer 403. The driver checks the pool first, so neither path touches a foreign VM.
15. `status/shutdown` with `forceStop=1` and `timeout`; `status/start` of a running VM fails (the driver checks the status first).
16. **[high]** `DELETE …?purge=1&destroy-unreferenced-disks=1` removes the guest, its disks and its pool membership.
17. A DHCP guest has no address without the guest agent (D-079).
18. **[high]** A cold restart (shutdown + start) applies a new SMBIOS serial; a reboot does not (D-078).
19. **[high]** Card checks: a reboot after enrollment works; a later change of a cloud-init field creates no new SSH host keys and no new first-boot run.
20. The task log endpoint `…/tasks/{upid}/log?start=0&limit=500` returns `[{n,t}]`.
21. **[high]** The node certificate verifies against `pve-root-ca.pem` for the host name or IP that the controller uses (SANs of the node certificate).
22. `onboot=1`, `description`, `name` need `VM.Config.Options`, which the role has.

## Result (owner, max 8 lines)
`pl-backend/T-012-proxmox-vm-driver` (offline part only) · tests: `make lint test build` → pass; `go test ./internal/driver/proxmox/...` uses an httptest fake PVE, no recordings yet.
- Client (D-076), driver with FetchImage/Create/Observe/Diff/Update/Power/Delete, pool + range guard (D-077), SMBIOS/cicustom/sshkeys/digest rules (D-078), IPs (D-079).
- `make test-pve` runs `host_test.go` (build tag `pve`) with the `PLUMB_PVE_*` variables; not run yet.
- Review fixes: pool filter on the answer, token scrubbed from PVE errors, UPID required where a task must start, bounded task wait with retries and task stop, input checks, image cleanup, exact `nextid` check, redacted client printing, no redirects, `PathUnescape`; host test removes its template.
- **Left for the host:** the 22 items above, recorded answers in `testdata/pve`, real-host checks of the card, `make test-pve`. `plumb serve` without `--dev` waits for the installer config (T-013).

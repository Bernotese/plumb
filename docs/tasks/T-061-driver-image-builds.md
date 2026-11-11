# T-061 – Image builds: Proxmox driver

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-065)
- **Size:** L   · **Depends on:** T-058; host work only when pl-lead names this card   · **Parent:** T-039

## Goal
The driver makes a build guest, turns it into a versioned template, finds it again and removes it safely.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §5 (all); host log A1 … A5, B1 … B4; `docs/decisions/T-039.md` D-203, D-208, D-209
- `internal/driver` (`proxmox/driver.go` clone/configure/Observe, `fake`), T-073 delete guard, recordings + leak scan

## Acceptance criteria
- [ ] `CreateBuild`: full clone, name `plumb-build-<name>-v<N>`, marker `plumb-build: <name> v<N>`, `onboot=0`, user `plumb-build` without key, `ciupgrade=1` when `upgrade`.
- [ ] `MakeImage`: guard (build marker, `stopped`, `template: 0`); one config write with the digest (delete the cloud-init values, `smbios1` without serial, `ciupgrade=0`, name `plumb-img-<name>-v<N>`, description with `plumb-image`, `version`, `build=<digest>`); stale digest refused; then convert.
- [ ] `FindImage` by name + full digest line in pool and range; `ListImages` with the unused import files; `GuestSpec.ImageVersion` → `plumb-image-version: <N>`, read by `Observe` (not in `Diff`). Every clone stays full.
- [ ] `RemoveImage` (n1): reads the pool list first (403 for a gone VMID); guard range, pool, `template: 1`, no lock, exact name (`regexp.QuoteMeta`), description with this Image and the full digest or checksum; `purge=1`, `destroy-unreferenced-disks=1`. Never an import file. `Driver.Delete` still never removes a template.
- [ ] Review S3: `RemoveImage` refuses a template with a linked clone in the pool (disk lines of pool VMs and `parent` of storage volumes).
- [ ] Review S5: `DiskSpace` gives size and real use of the disk storage, and thin-pool metadata use when the token can read it.
- [ ] `fake` models versions, linked clones and storage use.
- [ ] Host checks (only with go from pl-lead, rules of AGENTS.md; write to `docs/tasks/T-061.log.md`): recordings for the new calls; which of the two linked-clone signs lvmthin shows the token (else the limit goes into the docs); storage use and metadata use readable with the token. End state: nothing of this card left on the host.
- [ ] Tests: unit (guard with a name with regex characters, linked clone blocks, stale digest), replay with leak scan; `make test` passes.

## Out of scope
- When the engine calls these methods (T-062). Delete of import files (D-209).

## Result (owner, max 8 lines)
branch · commit · tests · notes

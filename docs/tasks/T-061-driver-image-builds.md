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

## Host step for pl-backend (pl-frontend wrote the code with fake PVE tests only)
Only with go from pl-lead, rules of AGENTS.md. Log to `docs/tasks/T-061.log.md`. End state: nothing of this card left on the host (`cleanupImageFlow` removes guests and templates by name; free the import file by hand with `pvesm free`).
1. Run `TestHostImageBuild` with `PLUMB_PVE_RECORD=<dir>` (make test-pve, or `-run TestHostImageBuild` in the LXC). It runs: DiskSpace, FetchImage, CreateBuild v1 (no upgrade), hard stop, MakeImage, FindImage, ListImages, Create of a Target from v1 (full clone, version marker), Observe, Delete, a linked clone by token (`full=0`), RemoveImage (must refuse), Delete of the linked clone, RemoveImage of the version and of the base template.
2. Copy the recording to `internal/driver/proxmox/testdata/pve/images/` after a leak check. `TestReplayImageBuild` skips until that directory exists, then replays it; `TestRecordingsHoldNoSecret` scans it.
3. Write the "host fact:" lines of the test log into the log:
   - DiskSpace: does `/nodes/<node>/storage/<disk>/status` give the real thin-pool use (compare with `lvs` Data%)? Does the token read `/nodes/<node>/disks/lvmthin` (needs Sys.Audit; 403 → `MetaKnown=false`)? If it reads it: is the pool matched by `lv_size == total`?
   - Linked clone: which sign does the token see: the disk line `local-lvm:base-<tpl>-disk-0/vm-<id>-disk-0` in the clone's config, the `parent` of the volume in `/storage/<disk>/content`, both or none? If none: write the limit into the docs (open point of the card).
   - Is `/storage/<disk>/content?content=images` readable by the token, and does it list only pool volumes? (RemoveImage refuses when it cannot read it.)
4. Check by hand on the template after MakeImage (before RemoveImage, e.g. with a breakpoint or a second run without the last steps): `qm config` shows none of `sshkeys ciuser ipconfig0 cicustom nameserver searchdomain onboot`, `ciupgrade: 0`, `smbios1: uuid=…` only, name `plumb-img-<image>-v1`, the 3 description lines. The `delete=` list has only keys that exist (does Proxmox refuse a delete of a missing key? the driver avoids it anyway).
5. VM names: Proxmox wants a DNS name. Check one long Image name (63 chars): `plumb-build-<name>-v1` is then 77 chars. If Proxmox refuses it, report it (the engine or the schema must limit the name).

## Out of scope
- When the engine calls these methods (T-062). Delete of import files (D-209).

## Result (owner, max 8 lines)
Code part (pl-frontend): branch `pl-frontend/T-061-driver-images` · tests: `make lint test build` → OK.
- `proxmox/images.go`: CreateBuild, MakeImage (guard, one write with digest, restore of build name+marker when the conversion fails), FindImage, ListImages (with linked-clone count), RemoveImage (pool list first, D-209 guard, `regexp.QuoteMeta`, linked clones from disk lines and volume `parent`, refuses when the volume list is unreadable), DiskSpace (storage status; thin-pool metadata via `/disks/lvmthin` when readable). Create writes `plumb-image-version: <N>`; Observe reads it.
- Contract: `ImageObject.Image` added (RemoveImage needs the Image name for the description guard).
- `fake`: versions, build guests, linked clones (`LinkClone`), storage use (`SetDiskSpace`), failure injection for the new ops.
- Fake PVE: config `delete`, `base-` volume after conversion, linked clones, storage status, `/disks/lvmthin`. `runImageBuild` runs on fake PVE now; `TestHostImageBuild` + `TestReplayImageBuild` wait for the host step above.
- Open: host step (pl-backend), docs of the linked-clone limit if the token sees no sign.

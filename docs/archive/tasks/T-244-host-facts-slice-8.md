# T-244 – Host facts for slice 8 (disks, the open restore items)

- **Owner:** pl-backend (the one agent on the host)   · **Reviewer:** pl-reviewer (reads the facts file and the log)   · **QA:** –
- **Size:** M   · **Depends on:** –

## Goal
Slice 8, child card **C8** of T-236. Facts from PVE 9.2 that the contract and the cards need, before code is written. Unlike T-214 this card makes test objects, so every action is listed.

## Allowed host actions (exactly these; anything else: stop and ask pl-lead)
Test guests are made **through Plumb** in the pool `plumb` (one Debian 13 VM, one Debian 13 LXC; a second VM on `vmbr9` for part 3). Calls that Plumb cannot make yet (a second disk, a mount point, the free of `unusedN`) are made **with the Plumb token against the PVE API** (curl from the controller's side with the token from its 0600 file, never on a command line), so each fact also proves "no new right". Root is used only to read (`qm config`, `pct config`, `pvesm list`) and for the cleanup at the end.
1. **Disks:** add a second disk to the pool VM (`scsi1` on the install's disk storage) and an `mp0` to the pool LXC with the token; back both up through Plumb; restore both; record: does the backup hold both disks, does the restore bring both; remove the VM disk → `unusedN`; free it with the token; restore a backup made **before** the disk was added: is the disk removed or left `unused`. Hot-add: with the guest running and stopped.
2. **The unproven list of #331** (T-221) that T-225 did not close: name each and its result.
3. **#335 L3 (a):** a test VM with a NIC on `vmbr9`; a Plumb backup; then an installer repair run **without** `--extra-bridge vmbr9 --extra-vlan vmbr9:20` (dry-run first, result to pl-lead, wait for the go): restore that backup → does PVE refuse before or after it removes the disks; does `vzdump/extractconfig` work with the token. Then the **revert**: the repair run with the usual flags again (dry-run, go), and the ACL view shows the `vmbr9` lines back.
- Each installer run: `--dry-run` first, result to `pl-lead`, wait for the go. `storage.cfg` copy before, byte-equal after. No change of the network config (vmbr9 stays as it is), SSH, `local`, `local-lvm`, roles.
- Cleanup at the end, with the go of `pl-lead`: this run's guests, volumes, templates, tokens; test secret files by D-416.

## Acceptance criteria
- [x] `docs/archive/plans/host-facts-slice-8.md` (≤ 80 lines, facts with how they were shown, no address / MAC / token id) and a short log `docs/tasks/T-244.log.md`; PR to `main`; report to `pl-lead` (max 8 lines). Part (1) is reported as soon as it is done (it unblocks T-238).

## Result (owner, max 8 lines)
`pl-backend/T-244-host-facts-slice-8` · PR #362 · facts `docs/archive/plans/host-facts-slice-8.md`, log `docs/tasks/T-244.log.md`.
- (1) Disks need no new right. Hot-add is live (VM and LXC). An LXC `mp` remove also leaves `unusedN`. The serial is not in `by-id`. An older backup: the VM keeps the disk as `unused0`, the LXC loses it. The LXC re-enroll is refused silently with a hand `mpN`.
- (2) `/status` is right; a stopped guest is backed up in `stop` mode; `WARNINGS` not caused. F1: two vzdumps → Plumb's own volume stays `unmarked` (T-248).
- L2: PVE never follows a symlink in an `mp` path. A refused hot-add leaks a volume; the token cannot free it, but a Plumb guest delete does (T-238).
- (3) The restore with a missing bridge right is refused before the disks are touched; `extractconfig` works with the token (pre-check, T-241). The way back works after the revert.
- Host clean: only LXC 100, `storage.cfg` byte-equal, 11 ACL lines (22 rows: user + token).

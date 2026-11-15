# Host facts for slice 8 (T-244, `pve-dev`, PVE 9.2.26)

Owner: pl-backend. Reviewer: pl-reviewer. Log: `docs/tasks/T-244.log.md`. A fact names how it was shown. Test guests were made through Plumb (controller `fbce9b5`): a Debian 13 VM and an unprivileged Debian 13 LXC in the pool `plumb`, disk storage `local-lvm` (lvmthin). Disk calls were made with the **Plumb token** against the PVE API, from the host, with the header in a 0600 file.

## Part 1 – more disks (D-423; unblocks T-238)

**Rights.** Every disk call below passed with the Plumb token. No new right is needed (D-423 holds): add, hot-add, remove, free `unusedN`, `vzdump/extractconfig`.

| # | Fact | How shown |
|---|---|---|
| D1 | VM hot-add works live: `POST …/qemu/<id>/config scsi1=local-lvm:2,serial=plumb-1` on the running VM gives no pending entry; the guest sees `sdb` at once. | `GET …/pending` has no `pending` key; `lsblk` in the guest |
| D2 | LXC hot-add works live: `PUT …/lxc/<id>/config mp0=local-lvm:1,mp=/srv/data,backup=1` on the running unprivileged CT is applied at once; PVE makes an ext4 and mounts it. | `GET …/pending`; `/proc/mounts` in the CT |
| D3 | A cold add (guest stopped) works for both (`scsi2`, `mp1`) and is there at the next start. | the same reads after the start |
| D4 | **The serial is not in `/dev/disk/by-id`.** On `virtio-scsi-single` (Plumb's `scsihw`), Debian 13 names the disk `scsi-0QEMU_QEMU_HARDDISK_drive-scsi1`. `serial=plumb-1` is visible only as `lsblk -o SERIAL` / udev `ID_SCSI_SERIAL`. A Block finds the disk by `lsblk -no NAME,SERIAL` or by the slot link `…drive-scsiN`. D-423 and the plan's `by-id/…plumb-<n>` text must change. | `ls -l /dev/disk/by-id`, `udevadm info` in the guest |
| D5 | A Plumb guest backup holds every disk: VM `#qmdump#map` lists `scsi0…scsi2`; LXC config lists `mp0`, `mp1` (`backup=1`). | `GET …/vzdump/extractconfig` with the token |
| D6 | A Plumb restore of that backup brings every disk back **with its data** (a marker written after the backup was replaced by the backup's marker; VM raw disk and LXC mp). VM: same volume names. LXC: new volume names (`disk-3…5`). | marker read in the guest after the restore |
| D7 | VM disk remove on a running VM (`delete=scsi2`) hot-unplugs live and leaves `unused0` (the volume stays). `delete=unused0` with the token frees the volume. | `qm config`, `pvesm list` |
| D8 | **LXC mp remove is not "freed at once":** `delete=mp1` on a running CT stays pending (`delete: 1`). After a restart, the volume is `unused0`, not freed. `delete=unused0` with the token then frees it at once (also on a running CT). So the LXC needs the same second step as the VM, and a remove on a running CT needs a restart (`disruptive` + `allow_restart`). | `GET …/pending`, `pct config`, `pvesm list` |
| D9 | **Restore of a backup made before the disks were added:** VM: the newer disk is **detached and kept** as `unused0` (the volume stays; its data was not read). LXC: the newer `mp0` volume is **gone** (data lost; the safety backup holds it). | `qm config`, `pct config`, `pvesm list` after the restore |
| D10 | **Re-enroll after a restore of an LXC with a hand-made `mpN` is refused, silently.** After restoring the B1 backup (with `mp0`, `mp1` that Plumb does not manage today), the follow-up re-enroll Plan failed: "the container has additions that were made outside Plumb: mp0 …, mp1 … (D-250)". Only an `ERROR` line in the journal. No Plan, no Issue: the CT stays stopped with a revoked credential (the D-427 class gap). | controller journal; `GET /plans`, `GET /issues` |
| D11 | Today's drift poll: an LXC `mpN` shows as `guest:config` `mounts` (destructive). VM `scsi1`/`scsi2` raise **no** Issue (extra VM disks are not observed). | `GET /issues` |
| D12 | Two restores in a row give two re-enroll Plans; approving the first makes the second `stale` (409). A person must re-propose (`POST /targets/<n>/reenroll`). | approve → 409 `stale` |

**What it means for T-238 (C2):**
- VM and LXC both free the volume in a second step (`delete=unusedN`); an LXC remove on a running CT is pending until a restart.
- Hot-add is live for both: an add is `reversible`, also on a running guest.
- D-423's serial text: say "visible as `lsblk SERIAL`", not a `by-id/…plumb-<n>` path.
- D9: on a VM restore Plumb finds the newer disk as `unusedN`. C2 must decide: re-attach it, or free it in the next Run. It must not make a new empty disk beside it. On an LXC the data is gone: the restore Plan must say so.
- D10: make the C2 `mpN` managed before slice 8 ships. For a hand-made `mpN`, C5 must open an Issue (or name it in the Run) when the re-enroll is refused.

## Part 2 – the unproven list of #331
(pending)

## Part 3 – #335 L3 (a)
- `vzdump/extractconfig` works with the token (D5). Restore with a missing bridge: (pending)

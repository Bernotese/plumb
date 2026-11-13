# Host facts for the coming topics (T-139)

pl-backend, 2026-11-13, test host `pve-dev`, PVE 9.2.26 (`pve-manager/9.2.26`, kernel 7.0.14), `libpve-network-perl` 1.6.8.
Read-only: `pvesh get`, `pveum … list`, `cat` of config files, API GETs with the Plumb token (from the host to 127.0.0.1:8006), and `grep`/`sed` of the installed PVE Perl code (`/usr/share/perl5/PVE`). Nothing on the host was changed.
No address, MAC, token id, key or password in this file.

Command classes: **[cfg]** `cat`/`pvesh get`/`pveum list` as root · **[tok]** API GET with the Plumb token · **[src]** installed PVE code · **[ct]** read-only command in LXC 100 (`pct exec`) · **[web]** public download site, from the dev VM.

## Baseline

- Token ACLs [cfg, tok `/access/permissions`]: exactly the 7 of D-081 + D-240, user and token on the same lines: `/` PlumbAudit (`Sys.Audit`, prop 0), `/nodes/pve-dev` PlumbNode (`Sys.AccessNetwork`), `/pool/plumb` PlumbGuests (prop 1), `/sdn/zones/localnetwork/vmbr0` PlumbBridge (`SDN.Use`, prop 0), `/storage/local-lvm` PlumbDisk, `/storage/plumb-images` PlumbImages, `/storage/plumb-snippets` PlumbSnippets.
- PlumbGuests has no `VM.Backup`, `VM.Console`, `VM.GuestAgent.*`, `VM.Config.CDROM`, `Pool.Allocate`.
- Storages [cfg]: `local` (dir: vztmpl, iso, backup, import), `local-lvm` (lvmthin: rootdir, images), `plumb-images` (dir: import, vztmpl), `plumb-snippets` (dir: snippets), `plumb-backup` (dir: backup). Pool `plumb` empty. Only guest: LXC 100 (Plumb, outside the pool).

## 1. Network: more bridges, more NICs, VLAN, SDN

- Host [cfg `/etc/network/interfaces`, `pvesh get /nodes/pve-dev/network`]: one physical NIC `nic0`, one bridge `vmbr0` (ports `nic0`, **not VLAN-aware**, no `bridge-vids`). No other bridge.
- SDN [cfg `/cluster/sdn/*`]: no zone, no vnet, no controller; only the default IPAM `pve`. `/etc/pve/sdn` holds no zone file.
- Rule [src `GuestHelpers.pm` `check_vnet_access`, used by `QemuServer::check_bridge_access` and `LXC::check_bridge_access`]: for every `netN` on create and update, and for the old value on a change:
  - no tag, no trunk → `SDN.Use` on `/sdn/zones/<zone>/<bridge>`;
  - tag T → `SDN.Use` on `/sdn/zones/<zone>/<bridge>/<T>` (an ACL on the bridge with propagate 1 also gives it);
  - `trunks` → `SDN.Use` on `/sdn/zones/<zone>/<bridge>/<T>` for every tag.
  - `<zone>` is `localnetwork` for a plain Linux bridge; for an SDN vnet it is the vnet's zone (path `/sdn/zones/<zone>/<vnet>`).
- A second NIC (`net1`) on VM or LXC: `VM.Config.Network` on the guest (we have it through `/pool/plumb`) + the `SDN.Use` above for its bridge. `ipconfig1` on a VM: `VM.Config.Cloudinit` (we have it).
- Today the token can use only `vmbr0` without a tag (PlumbBridge has propagate 0, D-081). The token sees only the bridges it may use: `GET /nodes/<node>/network` is `user => all` and lists a bridge only with `SDN.Audit`/`SDN.Use` on it [src `API2/Network.pm`, tok 200].
- A VLAN tag on the non-VLAN-aware `vmbr0` makes PVE use its classic per-tag bridge (`vmbr0v<T>`); not tried here (no change allowed).
- **Needs a new token right: yes**, for each extra bridge or vnet: `SDN.Use` on `/sdn/zones/localnetwork/<bridge>` (or `/sdn/zones/<zone>/<vnet>`), propagate 0; for VLAN tags: `SDN.Use` on `/sdn/zones/localnetwork/<bridge>/<tag>` per tag (narrow), or propagate 1 on the bridge (all tags). Same role `PlumbBridge`, more ACL paths. A second NIC on `vmbr0` without a tag needs no new right.

## 2. Adoption of existing guests

- What the token sees of a guest outside the pool (LXC 100 is one) [tok]:
  - `/nodes/<n>/lxc`, `/nodes/<n>/qemu`, `/cluster/resources?type=vm`: empty (filtered by `VM.Audit`).
  - `/nodes/<n>/lxc/100/config`, `/status/current`, `/interfaces`: 403 `Permission check failed (/vms/100, VM.Audit)`.
  - Visible through `Sys.Audit` on `/` (D-240): `/cluster/metrics/export` lists `lxc/100` with its use metrics; `/cluster/tasks` lists tasks of all guests. So Plumb sees the **ID and use** of a foreign guest, never its config.
  - `/cluster/nextid` works (answers the lowest free VMID).
- Moving a guest into the pool [src `API2/Pool.pm` update]: `Pool.Allocate` on `/pool/plumb` **and** `check_perm_modify` on `/vms/<id>` (`Permissions.Modify` or `VM.Allocate` on that guest). A guest in another pool also needs `allow-move` and `Pool.Allocate` on the old pool. The token has none of these on a foreign guest.
- After a person (root) adds the guest to `plumb`, PlumbGuests applies to it at once (propagated). Then Plumb has its normal rights; limits stay: disks on a storage other than `local-lvm` (no `Datastore.*` there), NICs on another bridge or with a tag (section 1), CD-ROM (`VM.Config.CDROM`), and LXC options that only root may set (memory note: privileged CT, mpN/devN).
- **Needs a new token right: no**, if a person moves the guest into the pool (UI or `pvesh set /pools/plumb --vms <id>`) and Plumb only reads and adopts what is in the pool. **Yes** (`Pool.Allocate` on `/pool/plumb` + `VM.Allocate` on `/vms/<id>` or `/`) if Plumb shall move guests itself; this is wide (on `/` it reaches every guest).

## 3. Guest backups (vzdump)

- Create backup [src `API2/VZDump.pm` POST `/nodes/<n>/vzdump`]: `VM.Backup` on each guest + `Datastore.AllocateSpace` on the backup storage (and on a fleecing storage, if used). Root only: `tmpdir`, `dumpdir`, `script`, `job-id`. `Sys.Modify` on `/`: `bwlimit`, `ionice`, `performance`. `Sys.Modify` on `/nodes/<n>`: `stop`. `Datastore.Allocate` on the storage: `prune-backups`.
- List backups [src `API2/Storage/Content.pm`, `Storage.pm`]: `Datastore.Audit` or `Datastore.AllocateSpace` on the storage; a backup volume is readable with `Datastore.AllocateSpace` + `VM.Backup` on its owner VMID.
- Delete one backup: `Datastore.Allocate` on the storage, **or** `Datastore.AllocateSpace` + `VM.Backup` on the owner VMID. Risk: the owner is only the VMID, and `nextid` reuses the lowest free VMID, so a new guest with an old VMID gets the old backups (read, delete, restore).
- Restore over an existing guest [src `API2/Qemu.pm`, `API2/LXC.pm`]: `VM.Backup` on that VMID is enough, plus `Datastore.AllocateSpace` on each target storage. Restore as a new guest: `VM.Allocate` (pool path works).
- Extract config of a backup: `Datastore.Allocate`, or `VM.Backup` + `Datastore.AllocateSpace`.
- Storage content `backup` exists on `local` and `plumb-backup` [cfg]. The token has no right on either: `/nodes/<n>/storage/plumb-backup/content` and `/status` → 403 (`Datastore.Audit|Datastore.AllocateSpace`); `/nodes/<n>/vzdump/defaults` → 403 (it checks the default storage `local`) [tok]. `/cluster/backup` → 200, empty (no backup jobs) [tok]. `/etc/vzdump.conf` has only comments [cfg].
- `plumb-backup` also holds the controller's backup copies in its subdirectory `plumb-backups/` (T-122); vzdump files go to `dump/`. A storage listing shows only `dump/` volumes.
- **Needs a new token right: yes**: `VM.Backup` (add to PlumbGuests on `/pool/plumb`) and `Datastore.AllocateSpace` (+ `Datastore.Audit` to list all and see status) on the guest-backup storage. Retention by `prune-backups` would need `Datastore.Allocate` on it; Plumb can prune itself by deleting single backups without that.

## 4. SSH repair fallback

- LXC 100 [cfg `pct config 100`, ct]: one NIC `eth0` on `vmbr0`, a static IPv4 /24 with a default gateway (also an IPv6 address by SLAAC). The test guests' static addresses are in the same /24 with the same gateway, so the controller reaches guests on `vmbr0` on-link (layer 2), not through the host.
- Host [cfg]: `ip_forward = 0`, no nftables rule, no `cluster.fw`. So no host filter between LXC 100 and a guest on `vmbr0`. (The dev VM → guest TCP 22 timeout of T-039 is outside this path.)
- In LXC 100 [ct]: `ssh` client present (`/bin/ssh`), `ssh-keygen` absent; `/root/.ssh` empty; no sshd config; user `plumb` (`/var/lib/plumb`, nologin) has no `.ssh`. **No controller SSH key exists today.**
- What reaches the guest today: VM: `ciuser` (bootstrap user of the Target), `sshkeys` = the Target's user keys only (driver `configureWith`); vendor snippet `plumb-vendor.yaml` has no SSH key (CA, agent URL, agent unit, install script, `ssh_deletekeys: false`). LXC: `ssh-public-keys` at create = the same user keys, written once by Proxmox (no later change through the API).
- Host keys: no API shows a guest's SSH host keys. Options: the Plumb agent reports them while it runs (the trust must exist before the fallback is needed), or cloud-init's console output.
- Proxmox guest agent as an alternative [src `API2/Qemu/Agent.pm`]: `exec`, `exec-status` need `VM.GuestAgent.Unrestricted`; `file-read`/`file-write` need `VM.GuestAgent.FileRead`/`FileWrite`; info calls need `VM.GuestAgent.Audit`. The driver sets no `agent` option on VMs today, and the stock cloud images are not checked for `qemu-guest-agent`. LXC has no exec API (`pct exec` is root on the host); a console needs `VM.Console`.
- **Needs a new token right: no** for SSH (a controller key put into `sshkeys`/cloud-init, `VM.Config.Cloudinit` we have; for an LXC only at create). **Yes** for the guest-agent path: `VM.GuestAgent.Unrestricted` on `/pool/plumb` (it is root in the guest), plus `VM.Config.Options` for `agent: 1` (we have it).

## 5. Signed image checksums [web, no host]

- Debian cloud images (`cloud.debian.org/images/cloud/<suite>/<date>/`): `SHA512SUMS` and a per-image `.json`, **no signature file** (`SHA512SUMS.sign` 404, none in `latest/` or parent dirs). Trust today = HTTPS + the pinned sum.
- Ubuntu cloud images (`cloud-images.ubuntu.com/releases/<suite>/release-<date>/`): `SHA256SUMS` + detached `SHA256SUMS.gpg` (also `MD5SUMS(.gpg)`). Signing key fingerprint `D2EB 4462 6FDD C30B 513D 5BB7 1A5D 6C4C 7DB8 7C81` (RSA, SHA-512 digest); the pinned 26.04 image is in `SHA256SUMS` (74 lines).
- LXC templates (`download.proxmox.com/images/system/`, plain http): no signature per file; the index `aplinfo-pve-9.dat.gz` with detached `aplinfo-pve-9.dat.asc` lists the `sha512sum` of each template. Signed by `Proxmox Trixie Release Key` `24B3 0F06 ECC1 836A 4E5E FECB A7BC D142 0BFE 778E` (in the host's `/usr/share/doc/pve-manager/trustedkeys.gpg`; `pveam update` checks it [src `APLInfo.pm`]). `aplinfo-pve-8.dat.asc` also exists.
- **Needs a new token right: no** (download and check happen in the controller or the installer).

## Open / not checked

- VLAN tag on non-VLAN-aware `vmbr0` (no change allowed). `qemu-guest-agent` in the stock cloud images. Ubuntu key name against a keyring package (`ubuntu-cloudimage-keyring`).

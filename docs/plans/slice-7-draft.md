# Slice 7 draft – "More of Proxmox" (prep, D-326 (3))

Status: **draft for the slice 7 plan card**. Not a decision, and no code. Written by pl-architect on 2026-11-14 from `main` after #303.
Inputs: ROADMAP rows 14, 15 · D-292, D-379 (U3), D-380 (U4), D-081 · host facts T-139 §1, §3 (`origin/pl-backend/T-139-prep-host-facts:docs/plans/host-facts.md`) · risks T-140 §6 (`origin/pl-reviewer/T-140-risk-draft:docs/plans/risks.md`) · `install/install.sh:360-386` (roles and ACL paths).
Marks: **USER** = the user must answer or act (D-326 (4)). **FACT** = check on `pve-dev` (read-only call) or in the PVE source before the contract.

## 0. What the user already decided
- **D-379 (U3):** the token may get `SDN.Use` on each more bridge (or VLAN zone path) that the person names at install. On `pve-dev`, one isolated bridge `vmbr9` without a port may be made for the proof, by a card of pl-lead (the only allowed change to its network config, AGENTS.md).
- **D-380 (U4):** guest backups by Plumb (`vzdump` per Target). The new rights are exactly `VM.Backup` on `/pool/plumb` and `Datastore.AllocateSpace` + `Datastore.Audit` on **one** backup storage that the person names. No `Datastore.Allocate`, no `Sys.Modify`. The docs recommend an encrypted storage (PBS with a client key). The user knows that the token can then read, overwrite (restore) and delete the backups of pool guests, and fill that storage.
- **U7 (open, USER):** the user's own install gets the new ACL lines only by a repair run of the installer that **the user** starts. Ask at the merge of the installer card, with the exact `--dry-run` text.

## 1. Part A – more NICs and VLAN tags (row 14)

### Facts (T-139 §1)
- PVE checks `SDN.Use` for every `netN` on create and update, and for the old value on a change. With no tag, the path is `/sdn/zones/localnetwork/<bridge>`. With tag T it is `/sdn/zones/localnetwork/<bridge>/<T>`; an ACL on the bridge with propagate 1 also gives every tag. Trunks need every tag.
- A second NIC on `vmbr0` **without** a tag needs **no** new right (`VM.Config.Network` comes through the pool, and `SDN.Use` on `vmbr0` exists).
- The token sees in `GET /nodes/<n>/network` only the bridges that it may use. So the UI can list exactly the usable bridges.
- `vmbr0` on `pve-dev` is not VLAN-aware. A tag on it makes PVE use the classic per-tag bridge `vmbr0v<T>`. That is not tried, because no host network change is allowed, and `vmbr9` is the proof instead.

### Proposal
- **Target schema:** keep `guest.bridge`/`ip`/`gateway` as NIC 0 (no migration of documents). Add `guest.nics`: a list of at most 3 more NICs, each `{bridge, tag?, ip?, gateway?}`. `ip` is `dhcp` or a CIDR; `gateway` only on one NIC in total. `tag` is 1 … 4094. `trunks` is not offered.
- **Driver:** `netN` (N = 1 … 3) = `virtio,bridge=<b>[,tag=<T>]`. For a VM, `ipconfigN` at create (cloud-init). For an LXC, `netN` holds `name=ethN,ip=…,gw=…`, and Proxmox applies it live.
- **Changes after create:**
  - adding or removing a NIC: a VM hot-plugs (risk `disruptive`); an LXC takes it live (`disruptive`);
  - a bridge or tag change of a NIC: `disruptive`;
  - the IP of a VM NIC after create: create-only, as NIC 0. The guest side of a later IP change goes through the `network` resource, extended to match a NIC by its MAC (today it knows only the main NIC, D-354).
- **MAC:** Proxmox picks it at create. Plumb keeps it on every update, as for `net0` (`checkUpdateParams`).
- **Adopted Targets:** `adoptable` stays one NIC (`nic-layout`) in slice 7. Widening it is a later card, after the NIC model is proven.
- **Server check before a Plan:** every bridge/tag of the document must be one the install has an ACL line for. The installer writes the list to the controller (`PLUMB_PVE_BRIDGES`). Else 422 with "this bridge needs an ACL line: run the installer with `--bridge …`". There is no runtime probe that would only fail late at PVE.
- **Firewall Block:** the nft table filters input on every interface today. Check this and say so in the Block text.

### Installer change (needs U7 for the user's own install)
- `--bridge vmbr0` (as today) plus repeatable `--extra-bridge <name>[:<tag>,<tag>…]`. One ACL line `PlumbBridge` per bridge path, and per tag path `/sdn/zones/localnetwork/<bridge>/<T>` (propagate 0, narrow). Never propagate 1 on a bridge (that gives all tags).
- `--dry-run` lists every new ACL line. A repair run removes lines of bridges that are no longer named (as T-129 does for the 7 lines).
- `pve-dev` proof: `vmbr9` made by its own pl-lead card (`ip link add vmbr9 type bridge`, or an `/etc/network/interfaces` stanza, **FACT/USER-adjacent**: pl-lead decides how, D-379), then `--extra-bridge vmbr9:20`.

### Risks (part A)
- **The token reaches another network:** every bridge and tag is a line that the person names; no wildcard; the server refuses unknown ones before PVE does.
- **A VM NIC hot-plug fails half way:** the driver re-reads the config, and the Run fails with the state shown. Test it with the fake driver and on the host.
- **The `network` resource and NIC N:** today it matches the main NIC by the route to the controller (D-354). For NIC N it must match by MAC, and the `permaddr` rule (D-370) holds. A wrong match is refused, with nothing changed.
- **The copy rule (D-331):** the new attributes are guest-config values, not agent reports, so they stay outside it, as today for `bridge`.

## 2. Part B – guest backups and restore (row 15)

### Facts (T-139 §3) and what to check
- **Create** `POST /nodes/<n>/vzdump`: `VM.Backup` on each guest + `Datastore.AllocateSpace` on the storage. Root-only options: `tmpdir`, `dumpdir`, `script`, `job-id`. `bwlimit`/`ionice`/`performance` need `Sys.Modify`, so Plumb does not send them. `prune-backups` needs `Datastore.Allocate`, so Plumb never sends it: retention is Plumb deleting single volumes.
- **List:** `Datastore.Audit` on the storage. **Delete one volume:** `Datastore.AllocateSpace` + `VM.Backup` on the **owner VMID**. **Restore over the guest:** `VM.Backup` on that VMID + `Datastore.AllocateSpace` on each target storage (the disk storage has it). **Restore as new:** `VM.Allocate` (the pool path has it).
- The owner of a backup is **only the VMID**, and `nextid` reuses the lowest free VMID. So a new guest on an old VMID "owns" the old backups (read, delete, restore) at the PVE level.
- **FACT 1:** does `GET /nodes/<n>/storage/<s>/content` return the `notes` of a backup volume, and may `POST /vzdump` set `notes-template` without root? (Plumb's identity marker depends on it; fallback: the `.notes` file through the content API, or the backup's `qemu-server.conf` via extract-config.)
- **FACT 2:** may the token read the storage config (`GET /storage/<id>`) to tell PBS with client-side encryption from a plain dir/NFS? If not, the person states it at install (`--backup-storage-encrypted yes|no`), and Plumb shows it.
- **FACT 3:** does a VM backup with the guest running need the QEMU guest agent for a consistent snapshot? We set no `agent: 1` (D-377 kept it off). Then `mode=snapshot` without fs-freeze is crash-consistent. Say so in the UI.

### Proposal
- **Target field `backup`:** `{schedule: daily|weekly + time (UTC), keep: 1 … 30}`. A missing field means no Plumb backups (as today). The storage is the one install-named storage (`PLUMB_GUEST_BACKUP_STORAGE`), never a document field.
- **Identity of a backup (VMID reuse):** every Plumb backup gets `notes-template` = `plumb:<install-id>:<target>:<binding-gen>`. `<binding-gen>` is a new counter per binding (or the VM `smbios1 uuid`/LXC `hostname` + creation time; **choose in the contract** after FACT 1).
  - Plumb lists, keeps, deletes and offers for restore **only** volumes whose marker names this install, this Target and its **current** binding generation.
  - A volume of an old guest on the same VMID is shown as "older guest on this VMID" and is never deleted or restored by a token path.
  - Retention deletes only marked volumes of the Target (the D-209 guard style).
- **Risks:**
  - backup Plan: `reversible` (the storage fills; the guest is unchanged; a snapshot backup does not stop it);
  - **restore over the guest: `destructive`, human-only** (new reason `guest-restore`);
  - restore as a new guest (after `guest:missing`): `destructive`, human-only;
  - delete one backup by hand: `destructive`, human-only.
- **Restore rules:**
  - The Plan names node, VMID, Target name, the volume, its marker and its time.
  - The Run checks the marker and the binding again before the restore; the guest must be stopped (an `allow_restart` rule, as D-194).
  - **After the restore:** the agent credential of the Target is revoked, and the guest re-enrolls by the reenroll path. An adopted Target uses the install command instead (D-392). So one live credential per Target (risks §6 rule 1).
  - The desired state is not rolled back by a guest restore: the next Run converges the guest to the head (the Plan says so).
- **Secrets in a backup (risks §6):** a vzdump holds the guest disk: the agent's credential (revoked by the restore rule above), the desired list with secret values (0600 in the guest), and SSH host keys (the repair pins stay valid on a restore over the same guest).
  - **USER:** the docs recommend PBS with client-side encryption. On a storage that is not known as encrypted (FACT 2), the first backup Plan of each Target carries a warning: "the backup holds secrets of the guest in clear".
  - Open question: no refusal by default (D-380 lets the user choose), or a refusal unless `--backup-storage-encrypted no` was stated knowingly at install. **Recommendation: the explicit install flag, no runtime refusal.**
- **The controller's own backups** (`plumb-backups/` on `plumb-backup`, slice 4) stay apart. If the person names the same storage for guest backups, vzdump goes to `dump/` and the controller's copy to `plumb-backups/`. Retention never touches the other subdir (test).

### Installer change (needs U7)
- `--guest-backup-storage <id>`: a new role `PlumbGuestBackup` = `Datastore.AllocateSpace Datastore.Audit` on `/storage/<id>` (propagate 0), plus `VM.Backup` added to `PlumbGuests`.
- `--dry-run` shows exactly these lines (D-380 wording). A repair run without the flag removes them again (T-129 style).
- **USER:** which storage on the user's own host. On `pve-dev`, `plumb-backup` (dir) is the proof storage. A PBS is not available there, so the encrypted path is shown by the warning text only (**FACT 2**).

### Risks (part B)
- **A token reads any pool guest's data from its backups:** accepted by D-380. Plumb's API offers no download of backup content.
- **VMID reuse:** the marker with the binding generation. Test with the fake driver: a new Target on an old VMID sees no old backup in its list and cannot restore it.
- **A restore makes two live agents:** the revoke at restore and the reenroll. Test: the old credential gets 401 after the restore Run.
- **Retention deletes foreign volumes:** only marked volumes of this install and Target. Test with a foreign volume of the same name pattern: untouched.
- **The storage fills:** keep at most 30 per Target, and a free-space check before each backup (`Datastore.Audit` gives the status), refused at < 10 % free, with the reason shown.

## 3. Cards (sketch; numbers from pl-lead)
| # | Title | Owner idea | Size | Depends on |
|---|---|---|---|---|
| C1 | Contract slice 7 (schema `nics`, `backup`; routes: list backups of a Target, back up now, restore, delete one; reason `guest-restore`; events) | pl-architect | M | FACT 1–3 |
| C2 | Installer: `--extra-bridge`, `--guest-backup-storage`, roles, dry-run, repair-run removal | pl-backend | M | C1 |
| C3 | Driver + plan: more NICs, tags, hot-plug, server bridge check | pl-backend | L | C1 |
| C4 | `network` resource for NIC N (match by MAC) | pl-backend | M | C3 |
| C5 | Driver + plan: vzdump, marker, list, retention, restore, revoke + reenroll | pl-architect (impl.) | L | C1, C2 |
| C6 | Web: NIC list in the Target form, backups tab on the Target, restore wizard | pl-frontend | M | C1 (mock) |
| C7 | QA (risks §6 tests, VMID reuse, foreign volume, two-agents) | pl-codex-qa | M | per card |
| C8 | `pve-dev`: make `vmbr9` (pl-lead card, D-379) | pl-backend | S | – |
| C9 | Host acceptance: NIC on `vmbr9` with tag 20 (VM + LXC), backup + restore of a VM and an LXC, old credential refused, retention, VMID reuse, leak grep of a vzdump for the credential (record the result) | pl-backend | M | all |

## 4. Open points for the plan card
- **USER (U7):** the repair run of the user's own install, with the new lines (`--extra-bridge …`, `--guest-backup-storage …`). Ask with the exact dry-run text when C2 is merged.
- **USER:** which bridges/tags and which backup storage on the user's own host (the answer goes into the installer flags; no code depends on it).
- **FACT 1–3** above. One read-only check run on `pve-dev` by the host card owner, before C1.
- **Team decision (no user):**
  - the binding-generation marker form;
  - `keep` maximum 30;
  - the free-space threshold of 10 %;
  - no `trunks`;
  - adopted Targets stay single-NIC in slice 7.

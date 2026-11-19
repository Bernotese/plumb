# Plan: the user's repair run (U7, U8): text to send

**Status:** draft for `pl-lead`. Nothing here is sent or run by an agent. `pl-lead` sends the text below the line to the user when the release is published (U6), with `<version>` filled in.

**Sources:** README "Install", "Updates and repair" and "The rights in detail" (the table that `install/readme_rights_test.go` checks against the installer); `docs/plans/release-slice.md` U7/U8; the T-263 log (the upgrade dry runs: 0 `pveum` lines when nothing changes).

**What the user's install is:** it was built from source at slice 6. It has none of the rights of slice 7 (guest backups, extra networks), and its state file holds none of the flags of T-265/T-269. Slice 8 adds no right.

---

## Update your Plumb to <version>: a repair run

Plumb `<version>` is out. Your install is from before guest backups and extra networks existed, so it needs one installer run (a "repair run"). It updates Plumb and sets the Proxmox rights to exactly what you ask for. Your guests keep running. Plumb itself stops for about a minute while its binary is replaced.

Please do steps 1–3 and send us the results of step 1 and step 3 **before** step 4.

### 1. Check what you have today (read only)

Run these on the Proxmox host as root. Replace `<plumb-vmid>` with the id of the Plumb container (`pct list` shows it as `plumb`).

```sh
# the Plumb version
pct exec <plumb-vmid> -- /usr/local/bin/plumb version
# the network of the Plumb container (the repair run must give exactly this)
pct config <plumb-vmid> | grep '^net0'
# the rights of the plumb user and its token today
pveum acl list | grep -E 'plumb@pve'
# the storages (to choose a guest backup storage, if you want one)
pvesm status
```

Then answer these questions for yourself (and send us the answers):

- **Extra networks:** do any of your Targets use a bridge other than the guest bridge (default `vmbr0`), or a VLAN tag? Lines with `/sdn/zones/localnetwork/…` in the `pveum` output show what Plumb may use today. A repair run sets this list from its options alone: **every extra bridge and VLAN tag must be named again**, or its right is removed.
- **Guest backups (new):** do you want Plumb to back up your guests? If yes, which storage? It must hold no guest disks and no snippets (the installer refuses the contents `images`, `rootdir` and `snippets`), and it must not be a storage of Plumb itself or the storage of Plumb's own backup copies.
  - On a **Proxmox Backup Server with an encryption key**, the backups are encrypted.
  - On any other storage (a directory, NFS, CIFS), the backups are **not encrypted**, and each one holds the whole guest disk with every secret in it. Then you must add `--guest-backup-clear` to say that you accept this.
- **The other options of your first install:** if you gave `--bridge`, `--disk-storage`, `--image-storage`, `--template-storage`, `--vmid-range`, `--nameserver`, `--searchdomain`, `--lxc-id`, `--backup-time`, `--backup-keep` or a backup copy option then, give them again. A repair run uses the defaults for every option it is not given.

Before the run, make a fresh backup of Plumb and keep your **recovery key** at hand:

```sh
pct exec <plumb-vmid> -- runuser -u plumb -- /usr/local/bin/plumb backup now --data /var/lib/plumb
```

### 2. Check the download

Download `plumb-<version>-linux-amd64.tar.gz` and `SHA256SUMS` from the release page.

1. **Who built it:** `gh attestation verify plumb-<version>-linux-amd64.tar.gz --repo Bernotese/plumb --signer-workflow Bernotese/plumb/.github/workflows/release.yml --source-ref refs/heads/main`. This needs `gh` 2.102.0 or newer, from https://cli.github.com; most distributions ship an older `gh` without `gh attestation`.
2. **The file is whole:** `sha256sum -c SHA256SUMS`.

Unpack it as root into a new folder that only root can write (for example `mkdir -m 700 /root/plumb-<version>`), and run the next steps from there.

### 3. The dry run (changes nothing)

Give the network of the Plumb container **exactly as the `net0` line of step 1 shows it**. The installer does not compare these flags with the container: it writes them into the cloud-init file of new guests, and a wrong address breaks the agents of every new guest without an error.

- `net0` has `ip=<address>/<prefix>` and `gw=<gateway>`: give `--lxc-ip <address>/<prefix> --lxc-gw <gateway>` with exactly these values.
- `net0` has `ip=dhcp`: leave out both `--lxc-ip` and `--lxc-gw`. The dry run then prints a `WARNING` about DHCP; that is expected, unless you want to change to a fixed address on purpose.

Then add the options from step 1:

```sh
bash install.sh --plumb-binary ./plumb --lxc-id <plumb-vmid> [--lxc-ip <address>/<prefix> --lxc-gw <gateway>] \
  [--extra-bridge <bridge>]… [--extra-vlan <bridge>:<tag>[,<tag>…]]… \
  [--guest-backup-storage <storage> [--guest-backup-clear]] \
  [the other options of your first install] \
  --dry-run
```

**How to read the result:**

- The first lines check the bundle (`SHA256SUMS matches …`), then `plumb version: <old> -> <version>`.
- Every line that starts with `+` is a command that the real run would do. The ones that change rights start with `+ pveum`.
- **The rights you should see added** (one line each, for the user `plumb@pve` and its token `plumb@pve!plumb`):

| You give                     | New `+ pveum` line                                                                                                                                                       |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `--guest-backup-storage <s>` | first the two new roles: `+ pveum role add PlumbGuestBackupVM --privs VM.Backup` and `+ pveum role add PlumbGuestBackup --privs Datastore.AllocateSpace,Datastore.Audit` |
| `--guest-backup-storage <s>` | `/pool/plumb` role `PlumbGuestBackupVM` (`VM.Backup`, inherited)                                                                                                         |
| `--guest-backup-storage <s>` | `/storage/<s>` role `PlumbGuestBackup` (`Datastore.AllocateSpace`, `Datastore.Audit`, not inherited)                                                                     |
| `--extra-bridge <b>`         | `/sdn/zones/localnetwork/<b>` role `PlumbBridge` (`SDN.Use`)                                                                                                             |
| `--extra-vlan <b>:<t>`       | `/sdn/zones/localnetwork/<b>/<t>` role `PlumbBridge` (`SDN.Use`), one line per tag                                                                                       |

- The rights your install already has (the pool, the disk storage, `plumb-images`, `plumb-snippets`, the guest bridge, the node, `Sys.Audit` on `/`) stay as they are. If they are right, the dry run prints no line for them.
- **Read this line with care:** "These older rights of Plumb are not in the plan and are removed:". It lists every right that the run would take away. That is right only for a bridge or VLAN that no Target uses any more. If it lists one that your Targets use, add that option and run the dry run again.
- `WARNING` and `NOTE` lines can come for several reasons: guest backups in clear (`--guest-backup-clear`), DHCP for the Plumb container, older backups of guests in Plumb's VMID range on the backup storage, or a storage without encryption given without `--guest-backup-clear` (then Plumb refuses every guest backup there). Send us every `WARNING` and `NOTE` line; we read them.
- The run must end without an error line.

### 4. What to send us, before the real run

- the output of step 1 (you may remove addresses);
- the dry run command you used (no passwords or tokens are in it);
- the dry run output from `plumb version:` to the end, or at least every `+ pveum` line and every `WARNING` and `NOTE` line.

We check it against the table above and answer "go" or what to change. **Then** you run the same command without `--dry-run`. The installer asks before it changes anything.

### 5. After the real run

- **Agents:** each Target's agent page shows its update state. Agents never update by themselves: use **Propose agent update** once per Target and approve it.
- **Guest backups:** with a guest backup storage, the Target pages show **Back up now** and the backup list.
- **The restore line:** from now on, `--remove --keep-guests` prints a complete restore line with every option of your install. Before this run, your install did not store its options, so that line would have ended with `<UNKNOWN …>`.

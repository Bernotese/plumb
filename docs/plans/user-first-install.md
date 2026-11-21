# Notes: the user's first install from the release

**Status:** draft for `pl-lead` (D-444: the user has no own installation yet, so the repair run of U7/U8 is dropped). `pl-lead` sends the text below the line to the user when the release is published, with `<version>` filled in. Nothing here is run by an agent.

**What the README already says** (these notes only point to it): the requirements; the download check (Install, step 1); the dry run and the real run (steps 2 and 3); the options for extra networks and guest backups; the recovery key; the rights table ("What the Plumb token may do"); the first login ("First steps"); the off-host copy of Plumb's backups ("Copy the backups off the host").

**What the README does not say yet, and these notes add:** (1) Install step 1 still says "Until there is a release, build the binary yourself"; (2) how to read the rights lines of the dry run; (3) that `--backup-storage` can be given at the first install. Proposal for `pl-lead`: put (1)–(3) into the README in the release PR; then these notes shrink to the decision list and the links.

---

## Install Plumb <version>

### Before you start: decide these four things

The installer sets the Proxmox rights from its options alone. A later run must name them again, so decide them now (README, Install, step 3):

1. **More networks for guests.** Does a guest need a bridge other than `vmbr0`, or a VLAN tag? Then name each one: `--extra-bridge <bridge>`, `--extra-vlan <bridge>:<tag>[,<tag>…]` (the bridge must be VLAN-aware). Without them, a Target can use only `vmbr0`.
2. **Guest backups.** Which storage holds the backups of your guests (`--guest-backup-storage <storage>`)? It must not hold guest disks or snippets. On a Proxmox Backup Server storage with an encryption key, the backups stay encrypted. On any other storage, Plumb makes guest backups only with `--guest-backup-clear`: then each backup holds the whole guest disk, **not encrypted**. Decide whether you accept that. Without the option, Plumb makes no guest backups.
3. **The copy of Plumb's own backups.** Plumb backs up its state every day inside its container. Name a storage on **another device** (`dir` on a mount, `nfs`, `cifs` or `cephfs`) with `--backup-storage <storage>` already at the first install, so the host copies each backup off the host (README, "Copy the backups off the host").
4. **The address of Plumb.** A free static IPv4 address and its gateway (`--lxc-ip <address>/<prefix> --lxc-gw <gateway>`). The agents in your guests keep this address.

### 1. Download and check

Download the tarball and `SHA256SUMS` from the release page. **Before you unpack**, check both as README "Install", step 1, and [Verify a download](../RELEASE.md#verify-a-download) say: `gh attestation verify` with the options there, and `sha256sum -c SHA256SUMS`. Then unpack as root into a folder that only root can write.

### 2. Dry run, and how to read its rights lines

```sh
bash install.sh --plumb-binary ./plumb --lxc-ip <address>/<prefix> --lxc-gw <gateway> \
  <your options from above> --dry-run
```

It must start with "Bundle check of release <version>". It changes nothing. Each line with `+` is a command it would run. For the rights, read the `+ pveum` lines:

- `+ pveum role add Plumb… --privs …`: one role per line of the README table "The rights in detail". The privileges must equal the table.
- `+ pveum acl modify <path> --users plumb@pve --roles …` and the same with `--tokens plumb@pve!plumb`: each table line twice, once for the user and once for its token.
- Without options: the 7 base lines (`PlumbGuests` … `PlumbAudit`), so 14 `acl modify` lines. Each `--extra-bridge` adds one line, each VLAN tag one line, `--guest-backup-storage` two lines (each twice again).
- A path or a role that is not in the table: stop and ask us. On a first install there is no "These older rights of Plumb … are removed" block.

Send us the dry run if you are unsure. It holds your addresses, but no secret.

### 3. Real run and the recovery key

Run the same command without `--dry-run` (README, step 3). Write the **recovery key** down when it is shown (step 4): offline, never on the host. Keep the printed admin password for the first login.

### 4. First login

Open `https://<plumb-address>:8443`, accept the warning of Plumb's own CA once, log in as `admin` and set a new password (README, "First steps"). Then check Settings → General: every rights line says "there". Settings → Backups shows the off-host copy after its first run (30 minutes after the daily backup).

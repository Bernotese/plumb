# Plumb

Plumb manages the virtual machines and containers on one Proxmox host. You run it yourself, in
your own network.

- **Images:** Plumb downloads cloud images and container templates. It can build its own Image
  versions from them, on a schedule, and keeps a fixed number of versions.
- **Targets:** a Target is one guest, a VM or an LXC container. Plumb creates it from an Image.
- **Blocks:** a Block is a reusable piece of configuration: files, packages, services, users and
  more. You add Blocks to a Target. An agent in the guest applies them.
- **Plans:** every change is a Plan first. The Plan shows each change and its risk. Nothing
  changes until you approve it.
- **Users:** each person has their own login and a role: read, plan, apply or admin.
- **Issues:** when a guest no longer matches its Target, Plumb opens an Issue. You revert the
  change, accept it, or ignore it.
- **History:** every approved change is a revision. You can see who changed what, and why.

You use Plumb in the web UI. Everything in the UI is also in the API. Guests run Debian 13 or
Ubuntu 26.04.

## Requirements

- Proxmox VE 9.1 or newer (qemu-server 9.2.11 or newer, libpve-storage-perl 9.1.13 or newer). The
  installer checks this and changes nothing on an older host.
- Root access to the host on a terminal (`ssh -t`). The installer asks questions and shows a key
  once.
- A storage for guest disks (default `local-lvm`) and a bridge (default `vmbr0`).
- A free IPv4 address for Plumb. Use a static address: the guests' agents keep this address. With
  DHCP, a new lease cuts off all agents.

## Install

### 1. Get the files

Until there is a release, build the binary yourself on a Linux machine with Go and Node.js 24 or
newer (versions: `go.mod`, `Makefile`):

```sh
git clone https://github.com/Bernotese/plumb.git
cd plumb && make build          # makes bin/plumb
```

Copy `bin/plumb` and `install/install.sh` to the Proxmox host.

### 2. Dry run

The dry run prints every command that would change the host. It changes nothing.

```sh
bash install.sh --plumb-binary ./plumb --lxc-ip <address>/<prefix> --lxc-gw <gateway> --dry-run
```

Read the list. Use `bash install.sh --help` for all options (storages, bridge, VMID range, DNS).

### 3. Real run

Run the same command without `--dry-run`. The installer asks before it changes anything.

It makes:

- an unprivileged Debian 13 LXC with the name `plumb` (2 cores, 1 GiB memory, 8 GB disk);
- a pool `plumb` for your guests, a user `plumb@pve` with an API token, and roles for it;
- the storages `plumb-snippets` and `plumb-images` (two directories on the host).

### 4. The recovery key

During the install, Plumb shows a **recovery key** once. Plumb does not store it.

- Write it down and keep it offline, for example in a password manager or on paper.
- Without it, Plumb cannot read its stored secrets again after a restore.
- Do not put it in a file on the Proxmox host or in a Plumb guest.

### What the Plumb token may do

Plumb never gets root access to Proxmox. Its token may:

- create, change, start, stop and delete guests in the pool `plumb`;
- use space on the disk storage and on `plumb-snippets`, and connect guests to the guest bridge;
- download images and templates into `plumb-images`.

It can also read, but never change, data of the whole host. It needs this right (`Sys.Audit` on
`/`) to see the free space of the disk storage, and Proxmox gives no narrower right for it. So
Plumb can read:

- the IDs, names and CPU, memory and disk use of **all** guests, also outside the pool;
- the task list of all users, root included (not the task logs);
- the backup jobs and the guests in them;
- the host and datacenter settings: disks and SMART data, `/etc/hosts`, the node config, and the
  cluster firewall rules.

It cannot read the configuration of a guest outside the pool, the syslog or the network settings.

It may not:

- change guests outside the pool;
- change the host, its network, its storages or its users;
- give rights to anybody, or make new pools;
- open a console or use the QEMU guest agent.

The Plumb LXC itself is not in the pool, so Plumb cannot change itself. The installer prints the
full list of rights.

## First steps

1. Open `https://<plumb-address>:8443`. The certificate comes from Plumb's own CA, so your browser
   warns you once. Plumb is for your local network only.
2. Log in as `admin`. The installer prints the first password once. If you lost it:
   `pct exec <plumb-vmid> -- journalctl -u plumb.service | grep Password`.
3. Set a new password. Plumb asks for it at the first login.
4. **Images:** add an Image (a Debian or Ubuntu cloud image, or a container template). You can
   switch on a build with Blocks and a schedule.

   ![Image page of ubuntu-26.04: title, type virtual machine, kind stock, base image address and checksum](docs/screenshots/readme-images.png)
   <br>*An Image: where the base image comes from, and its checksum.*

   An Image keeps a fixed checksum (the pin). To move it to a newer file, choose a **signed
   checksum source** on the Image:
   - Ubuntu cloud images: Plumb checks `SHA256SUMS` with the Ubuntu key that is built into Plumb.
   - Proxmox container templates: Plumb checks the appliance index with the Proxmox key that is
     built into Plumb.
   - Debian cloud images: Plumb reads `SHA512SUMS` over HTTPS. Debian does not sign this list.

   Then click **Refresh base** on the Image. When the file changed, Plumb proposes a Plan that
   changes only the checksum. Existing Targets do not change; new Targets and new builds use the
   new file.

   ![Image page of ubuntu-26.04 with the checksum source "Ubuntu cloud images, signed SHA256SUMS" and the card Refresh base](docs/screenshots/readme-image-verify.png)
   <br>*An Image with a signed checksum source, and the "Refresh base" button.*

5. **Blocks:** the **starter catalogue** on the Blocks page lists the Blocks that ship with Plumb
   (for example `deploy-user`, `ssh-hardening`, `nginx`, `docker-engine`, `firewall`, `network`).
   "Add" copies one into your Blocks. When your copy differs from the catalogue, "Show difference"
   shows how, and "Update from the catalogue" replaces your copy. Both make a normal Plan. Plumb
   never adds or changes a Block by itself. Change the Blocks or write your own.
   - The `firewall` Block drops incoming traffic except the ports you list. SSH always stays open.
   - The `network` Block adds addresses, routes, name servers and search domains to the network
     card of a VM. The main address, the gateway and DHCP do not change.
   - When a `firewall` or `network` change cuts the guest off from Plumb, the agent puts the old
     settings back after 90 seconds.
   - A Block can also hold a local `group` (`groupadd`). Users join it through their `groups`.

   ![Starter catalogue: motd and nginx are added but differ, with Show difference and Update from the catalogue; network is not added, with Add](docs/screenshots/readme-catalogue.png)
   <br>*The starter catalogue: "Add" and "Update" make a Plan. Plumb never changes a Block by itself.*

   ![Block page of nginx: the Targets that use it, its parameter server_name, and the start of its YAML document](docs/screenshots/readme-blocks.png)
   <br>*A Block: the Targets that use it, its parameters and its document.*

6. **Targets:** make a Target. Choose the Image, the size and the Blocks. Later you can change
   these on the Target, also through a Plan:
   - **Power:** "running" or "stopped". A stop needs "Plumb may restart this VM" (for a container:
     "this container") on the Target.
   - **Tags:** the Proxmox tags of the guest. Plumb keeps exactly these. With no entry, Plumb does
     not touch the tags of the guest.
   - **Disk:** it only grows. Proxmox cannot make a disk smaller, so a grow cannot be undone.

   ![Targets list with four Targets: app1 in Plumb; db1, web1 and web2 out of Plumb; each with image, size, address and agent version](docs/screenshots/readme-targets.png)
   <br>*The Targets: one is in Plumb, three have drift.*

   ![Edit page of Target web2: configured and reported address, image, type, and the guest settings cores, memory, disk, bridge and power](docs/screenshots/readme-target.png)
   <br>*One Target: its address, its Image and its guest settings.*

7. **Plan:** "Show the plan" opens the Plan. Read it, then click "Approve and apply". The Run shows
   each step. The Plan shows two things:
   - **Document changes:** what changes in your Images, Blocks, Targets and rules, as a diff. Secret
     values are never shown, only the names of the secrets.
   - **Targets:** each change on each guest, with its old value, new value and risk. A change to a
     Block that no Target uses has document changes only.

   ![Plan 2 for web1: reason, risk disruptive, three resource changes and one restart with old and new values, and the Approve form](docs/screenshots/readme-plan.png)
   <br>*A Plan: every change with its old value, new value and risk. Nothing changes before you approve it.*

   ![Run 2 succeeded: the four steps on web1, each applied](docs/screenshots/readme-run.png)
   <br>*The Run of that Plan: each step on each Target.*

   ![Plan 4, an update of the Block motd from the catalogue: no resource changes, and the diff of the Block document](docs/screenshots/readme-plan-documents.png)
   <br>*A Plan that changes only a Block: its document changes, line by line.*

8. **Issues:** when somebody changes a guest by hand, an Issue opens. Choose "Revert", "Accept" or
   "Ignore". A Plan that accepts a value from the guest needs a person: an API token cannot approve
   it. The same holds for every Plan that takes a value that a guest reported into the desired
   state, also when you type it yourself: only a person can approve it.

   ![Issue 4: the file /etc/motd on web2 was edited by hand; the difference, and the buttons Revert and Accept as raw for this Target](docs/screenshots/readme-issue.png)
   <br>*An Issue: someone edited a file in the guest by hand. Revert it, accept it, or ignore it.*

In **Settings** you make API tokens, store secrets, and export or import the whole state.

### Users and roles

An admin adds people in **Settings → Users**. Each user has one role:

| Role | May |
|---|---|
| read | See everything. Change nothing. |
| plan | Also propose changes (Plans), comment on Issues, and drop Plans. |
| apply | Also approve Plans up to the risk "disruptive", ignore drift once, and start a backup now. |
| admin | Everything: destructive Plans, users, API tokens, secrets, an export with secret values, import, and the confirm after a restore. |

- Plumb makes a one-time password for a new user and shows it once. The user must change it at the
  first login.
- Plumb never deletes a user. Disable it instead. A name is never given twice.
- A role change ends every session of that user.
- The web UI hides the buttons that your role may not use. Plumb also checks every call itself.

![Settings, Users tab: admin, dev, ops with role apply and viewer with role read, each with the actions Role, Disable and One-time password, and the form for a new user](docs/screenshots/readme-users.png)
<br>*Settings → Users: each person with their role and state.*

## Updates and repair

To update Plumb, run the new `install.sh` with the new `plumb` binary. Use the same options as at
the install. The same run repairs a broken install. Try `--dry-run` first.

Agents in the guests update through a Plan: Plumb proposes it, you approve it.

- An agent that does not reach Plumb within 5 minutes after the update goes back to its old
  version by itself.

**Guests made before the agent self-update existed** need one root command per guest, once:

```sh
# LXC, on the Proxmox host:
pct exec <vmid> -- sh -c 'systemctl stop plumb-agent.service && /usr/local/sbin/plumb-agent-install'
# VM, over SSH or the console, as root:
systemctl stop plumb-agent.service && /usr/local/sbin/plumb-agent-install
```

After it, the Target page shows the new agent version. A guest without
`/usr/local/sbin/plumb-agent-install` must be made again.

An update never changes a stored Block. If your `nginx` or `nginx-site` Block answers 403, replace
its `index index.html;` line with `index index.html index.htm index.nginx-debian.html;`.

To remove Plumb: `bash install.sh --remove`. It refuses while the pool still holds guests or
templates. Delete them through Plumb first.

## Backup and restore

### What a backup holds

Plumb backs up its whole state every day at 03:00 UTC and keeps the last 7 backups (installer
options `--backup-time` and `--backup-keep`). **Settings → Backups** shows them, and "Back up now"
makes one at once.

![Settings, Backups tab: daily at 03:00 UTC, keep 7, the last run, the off-host copy state and the list of backup files](docs/screenshots/readme-backups.png)
<br>*Settings → Backups: the schedule, the last run, the off-host copy and the backups.*

A backup is encrypted. Only your recovery key opens it.

- It holds: every Image, Block, Target, rule and revision, the history, the Issues, the users and
  tokens, which guest belongs to which Target, and the identities of the agents.
- It does not hold: the recovery key, the Proxmox token of Plumb (the installer makes a new one),
  and the guests themselves. Back up the guests with Proxmox.

### Copy the backups off the host

The backups live in the Plumb LXC. If the disk of the host dies, they die with it. Copy them to a
storage on another device, for example an NFS or CIFS share:

```sh
bash install.sh --plumb-binary ./plumb <your usual options> --backup-storage <storage>
```

The host copies the newest backup to `<storage path>/plumb-backups/` every day (30 minutes after
the backup) and keeps 14 copies. The storage must exist in Proxmox (`dir`, `nfs`, `cifs` or
`cephfs`). A directory on the same host is not an off-host copy. Settings → Backups shows the state
of the last copy. `--backup-storage none` stops the copies; the copies stay.

Check a copy now and then on the host. It asks for the recovery key and changes nothing:

```sh
./plumb backup verify <storage path>/plumb-backups/<file>.pbk
```

This proves that the file opens with your key. Only a real restore proves the whole way back.

### Replace the controller

To move Plumb to a new LXC, or after the Plumb LXC broke, keep the guests and restore:

1. Remove only the controller. Plumb takes a fresh backup, copies it to the backup storage, and
   checks it with your recovery key. A wrong key stops here, before any change. The pool and its
   guests stay.

   ```sh
   bash install.sh --remove --keep-guests --plumb-binary ./plumb
   ```

   Without `--backup-storage` at the install, add `--backup-to <directory>`: a directory on this
   host. That is not an off-host copy; the installer warns you.
2. Restore. The installer prints the first part of this line at the end of step 1. Use the same
   address as before: the agents in the guests know only this address.

   ```sh
   bash install.sh --plumb-binary ./plumb --restore <file>.pbk --lxc-ip <address>/<prefix> --lxc-gw <gateway>
   ```

   The installer reuses the kept backup storage. It asks for the recovery key and checks the
   file before it changes anything. A wrong key stops the run.

### After a restore

- The installer shows a new password for `admin` once. Write it down. The first login asks for a
  new one. The old passwords do not work any more.
- Plumb starts in **restored mode**: it reads Proxmox and the agents, but it changes nothing. No
  Plan is applied and no backup runs. A banner says so.
- Every API token is disabled. Enable only the ones you still need, in Settings → API tokens.
- When the old controller is off for good, confirm it in **Settings → Backups**. Only then does
  Plumb work normally again. If two controllers run, both change the same guests.
- Guests that were made after the backup are listed as unknown guests. Plumb never changes or
  deletes them. A guest that was deleted after the backup gets an Issue; Plumb does not make it
  again by itself.

### Export and import

**Settings → Export and import** saves one YAML file with every Image, Block, Target and rule. It
does not hold the history, the Issues, the users or the tokens. It does not hold which guest belongs
to which Target, or the agent identities: a new Plumb made from it creates new guests. Secret
values are in it only when you give a passphrase.

Keep the recovery key in any case.

## Known limits

- A **stock Ubuntu 26.04** image used directly by a Target can stay on DHCP until its first
  restart. Use DHCP, or let Plumb build the Image: a built Image does not have this limit.
- The **5 minute agent guard:** when Plumb is not reachable for more than 5 minutes right after an
  agent update, the agent also goes back to its old version. A new update Plan installs it again.
- A change of cores or memory of a running VM waits for a restart. Switch on "Plumb may restart
  this VM" on the Target if Plumb may restart it.
- One Proxmox host only. No clusters.
- A restored Plumb needs the same address as before.
- A guest has one network card. The `network` Block adds addresses and routes on it; more cards
  come later.
- A live change by hand with `ip` or `nft` in a guest is seen at the full check every 15 minutes,
  not at once. A change of a file that Plumb writes, such as its netplan file, is seen at once.
- The first signed refresh of an Image needs a person: an API token cannot approve it.
- Debian does not sign its cloud image checksums. Every new checksum of a Debian Image needs a
  person.

## More

- [docs/BRIEF.md](docs/BRIEF.md): what Plumb is for.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): how it works inside.

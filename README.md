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

There is no release download yet. Build the binary on a Linux machine with Go and Node.js 24 or
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

- create, change, start, stop and delete guests in the pool `plumb` only;
- use space on the disk storage, the `plumb-images` and `plumb-snippets` storages, and the guest
  bridge;
- read status information of the host (read only).

It may not:

- change the host, its network, its storages or other users;
- give rights to anybody, or make new pools;
- open a console or use the QEMU guest agent;
- touch guests outside the pool. The Plumb LXC itself is not in the pool, so Plumb cannot change
  itself.

## First steps

1. Open `https://<plumb-address>:8443`. The certificate comes from Plumb's own CA, so your browser
   warns you once. Plumb is for your local network only.
2. Log in as `admin`. The installer prints the first password once. If you lost it:
   `pct exec <plumb-vmid> -- journalctl -u plumb.service | grep Password`.
3. Set a new password. Plumb asks for it at the first login.
4. **Images:** add an Image (a Debian or Ubuntu cloud image, or a container template). You can
   switch on a build with Blocks and a schedule.
5. **Blocks:** look at the starter Blocks (for example `deploy-user`, `ssh-hardening`, `nginx`,
   `docker-engine`). Change them or write your own.
6. **Targets:** make a Target. Choose the Image, the size and the Blocks.
7. **Plan:** "Show the plan" opens the Plan. Read it, then click "Approve and apply". The Run shows
   each step.
8. **Issues:** when somebody changes a guest by hand, an Issue opens. Choose "Revert", "Accept" or
   "Ignore".

In **Settings** you make API tokens, store secrets, and export or import the whole state.

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

Backup and restore of Plumb come with the next release.

## Known limits

- A **stock Ubuntu 26.04** image used directly by a Target can stay on DHCP until its first
  restart. Use DHCP, or let Plumb build the Image: a built Image does not have this limit.
- The **5 minute agent guard:** when Plumb is not reachable for more than 5 minutes right after an
  agent update, the agent also goes back to its old version. A new update Plan installs it again.
- A change of cores or memory of a running VM waits for a restart. Switch on "Plumb may restart
  this VM" on the Target if Plumb may restart it.
- One Proxmox host only. No clusters.

## More

- [docs/BRIEF.md](docs/BRIEF.md): what Plumb is for.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): how it works inside.

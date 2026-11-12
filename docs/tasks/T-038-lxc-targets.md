# T-038 – LXC targets   (test host ready; step 1: host questions, then the plan)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** test host (D-041), T-012 on `main`, T-039 (template with the agent)

## Goal
A Target with `type: lxc` is created from a Plumb template, enrolls with the `env` token and is configured by the agent like a VM.

## Design (decision level – D-097, ARCHITECTURE §5, §6, §12)
- The agent is in the template. The controller checks the Proxmox base template against the checksum of the Proxmox appliance index, repacks it and uploads it through the API. No host access.
- Enrollment: `env` option before the first start, read from `/proc/1/environ`, removed after enrollment.
- `guest:config` owns hostname and network of a container. Kernel-level resources are VM-only.

## Before this card starts
- [ ] `pl-architect` turns the design into a plan with the facts from the host: upload limit and file names for `vztmpl`, clone type per storage, rights of the `Plumb` role for containers, the zstd dependency (own decision line).
- [ ] The card then gets files, acceptance criteria and tests. Needs new architecture decisions.

- (added by pl-lead, from the PR #73 review) The `symlink` resource uses `renameat2` flags (`RENAME_NOREPLACE`, `RENAME_EXCHANGE`). For LXC on ZFS they need ZFS ≥ 2.2 on the host. Check it on the test host and define the behaviour when the flags are not supported (clear error, no unsafe fallback).

## Host questions (pl-architect, 2026-11-12; pl-backend answers them on `pve-dev`)
The plan needs these facts. Write the answers to `docs/tasks/T-038.log.md`: one row per id (`PASS`/`FAIL`/value, time, exact error text). Redact addresses, MACs, token ids and the host name of the controller. No code change, no installer run.

**Known, do not test again:** disk storage `local-lvm` (lvmthin), image storage `plumb-images` (dir, content exactly `import`, D-183), no ZFS on the host; rights of the token = the six roles of D-081 (`install/install.sh:252`); the agent reports every up interface when only a `veth` is left (D-216); `seal` knows that a container bind-mounts `/etc/hostname` and `/etc/hosts` (`internal/agent/sealfs.go:389`); image builds and full clones of VMs (T-039 log, T-066 log).

**Allowed on the host:** read-only commands; test containers in the Plumb pool and the VMID range, made by root `pct` or by the token, with the guard of T-055 (in range, in the pool, exact name `t038-…`); root `pct start|shutdown|stop|set|template|clone|destroy|exec|push` on these test containers only; `pveam available` and `pveam download` into `local` (a template file is content, not a storage change; remove it at the end if it was not there before); `pvesm free` of files of this card by exact name. Calls with the Plumb token: `curl` from inside the controller LXC with the settings of `/etc/plumb/plumb.env`, never printed.
**Needs the go of pl-lead first:** G0 (content `vztmpl` on `plumb-images`, reverted at the end). Not allowed: a new storage, a new right for the token (report the missing right and the exact 403 text instead), anything from the "never" list of AGENTS.md. End state: 0 test containers, 0 templates, 0 files of this card, `plumb-images` content as before.

### A. Base templates and the checksum source
- **A1** `pveam update; pveam available --section system`: exact file names and versions for Debian 13 and Ubuntu (is there a 26.04 `standard` template? if not: the newest Ubuntu). Compression of each (`.tar.zst`, `.tar.xz`, `.tar.gz`)? Size compressed.
- **A2** The appliance index: URL that `pveam` reads (`/var/lib/pve-manager/apl-info/`), which checksum field it holds (`sha512sum`? `md5sum`?), how `pveam` verifies the index itself (signature file, key). Is there a stable `https://` URL for the template file and for the index?
- **A3** Inside the archive (`tar -tv --zstd` plus `--xattrs`, with GNU tar on the host or the dev VM): count of entries per type (file, dir, symlink, hardlink, char/block device, fifo), any sparse entries, PAX/GNU long names, xattrs (`security.capability` on `ping` etc.), owners other than 0, the record size, and what follows the last entry (zero blocks, padding).
- **A4** From inside the controller LXC: can it reach the template URL over HTTPS (time to download Debian 13)? Free space of the controller root fs and of `/var/lib/plumb`; size of the root disk of the controller LXC.

### B. Upload as `vztmpl` (rights of today)
- **G0** (needs the go) `pvesm set plumb-images --content import,vztmpl`. Does the controller start check of D-183 then refuse the storage (expected: yes, "exactly import")? Revert at the end.
- **B1** With the token: `POST /nodes/<node>/storage/plumb-images/upload` (`content=vztmpl`, multipart) of the stock Debian 13 file. Works with `PlumbImages` (`Datastore.AllocateTemplate`)? Is the `checksum` / `checksum-algorithm` parameter accepted (sha256)? Time. Where does pveproxy buffer the upload (`/var/tmp`?) and is there a size limit? Does a chunked body (no `Content-Length`) work?
- **B2** File names: which names does the upload accept (`plumb-lxc-<name>-<sha12>.tar.gz`, `.tar.zst`, `.tar.xz`, upper case, a second dot)? Exact error for a refused name. Does an upload over an existing name fail or overwrite?
- **B3** The same upload with the file recompressed as `.tar.gz` and as an uncompressed `.tar` (root, on the host): does `pct create` accept both?
- **B4** With the token: `DELETE …/storage/plumb-images/content/plumb-images:vztmpl/<file>` of a file the token uploaded. Works? If not: exact error and the right that Proxmox names. Same for `GET …/content?content=vztmpl` (listing works?).
- **B5** `download-url` with `content=vztmpl` and the token (only to know the rights; Plumb will not use it for a repacked file): works or exact 403?

### C. Create, clone, template (rights of today)
- **C1** With the token: `POST /nodes/<node>/lxc` from the uploaded file with `vmid`, `pool`, `hostname`, `unprivileged=1`, `features=nesting=1`, `rootfs=local-lvm:8`, `net0=name=eth0,bridge=<bridge>,ip=dhcp` (and once `ip=<PLUMB_TEST_VM_IP_1>/<cidr>,gw=…`), `nameserver`, `searchdomain`, `ssh-public-keys`, `cores`, `memory`, `swap`, `onboot=0`. Works? Which parameter is refused, with the exact text (expected candidates: `features`, `nameserver`)? Time until the task ends.
- **C2** The `env` option (D-025): exact name and syntax in PVE 9.2 (`pct set <id> --env …`? `lxc.environment` is root-only raw config). Can the token set it at create and with `PUT …/lxc/<id>/config`? Which right does Proxmox check? Is it in `GET …/config` for a `VM.Audit` reader (it is a secret there)? Is it shown in the task log or in `/var/log/pve/tasks`?
- **C3** In the container with `env` set (root `pct exec`): is the value in `/proc/1/environ`? Mode and owner of that file; can a non-root user in the container read it? Does systemd pass it to services (`systemctl show-environment`, `/proc/<pid of a unit>/environ`)?
- **C4** Remove `env` on a **running** container (token, `delete=env`): applied at once or `pending` (`GET …/pending`)? `/proc/1/environ` after it, and after `pct reboot` and after a cold stop and start. Does a pending `env` show in `GET …/config` or only in `…/pending`?
- **C5** Live changes on a running container with the token: `cores`, `memory`, `swap`, the `bridge` of `net0`, `hostname`, `ip` of `net0`. For each: applied at once, pending, or refused? Exact error. Does `hostname` reach `/etc/hostname` without a restart?
- **C6** Conversion and clone with the token: `POST …/lxc/<id>/template` on a stopped container, then `POST …/lxc/<tpl>/clone` with `full=1`, `pool`, `hostname`, `target storage local-lvm`. Works? Times: create from archive vs full clone of the template. Does a linked clone (`full=0`, root) leave a sign that the token can see (D-221)? `DELETE …/lxc/<tpl>` with a full clone alive.
- **C7** `GET …/lxc/<id>/interfaces` and `GET …/lxc/<id>/status/current` with the token: does the answer hold the addresses? Same for a container whose agent is not running.
- **C8** `DELETE …/lxc/<id>` with `purge=1` (and `destroy-unreferenced-disks=1`): works with the token; volumes gone; anything left in `/var/lib/lxc/<id>` (compare PR #98)?
- **C9** `GET /access/permissions` of the token: the full list of rights on the pool path, so the plan names exactly the missing ones.

### D. Inside an unprivileged container (Debian 13 and the Ubuntu of A1)
- **D1** What Proxmox writes at create, at clone and at every start: `/etc/hostname`, `/etc/hosts`, `/etc/resolv.conf`, network files (`/etc/network/interfaces`, `/etc/systemd/network/*.network`, netplan?), `/root/.ssh/authorized_keys`, SSH host keys (made new at create? at clone?), `/etc/machine-id` (empty, `uninitialized`, a value; new at clone?). Which `.pve-ignore.*` files exist?
- **D2** Static address: the `ip=` address answers at the **first** start (neighbor table of the controller LXC, as in T-015), and after a reboot. DHCP: lease, time to the first address.
- **D3** The agent as root (copy `bin/plumb-agent` with `pct push`, run it by hand with a test config that points at nothing): `id`, `/proc/self/uid_map`, the capability set (`/proc/self/status` `CapEff`), the file system of `/` (`stat -f`), `renameat2` with `RENAME_NOREPLACE` and `RENAME_EXCHANGE` (the `symlink` resource test of the agent, or a tiny test binary), netlink read of addresses and link kinds (what kind is `eth0` from inside?).
- **D4** systemd units: does a unit like `plumb-agent.service` start at boot with `nesting=1`? Without `nesting=1`? `systemctl --failed` in a fresh container with and without nesting. Do `apt-get install nginx`, `useradd -m`, `chown` to a high uid (65534), `systemctl restart nginx` work?
- **D5** `fstrim`, `journalctl --vacuum-time`, `apt-get clean`, `/var/lib/systemd/random-seed`, `/var/lib/dbus/machine-id`: which exist and which fail in a container (the `seal` steps of D-206)?

### E. Space and time
- **E1** Space: data use of the rootfs volume of a fresh Debian container (`lvs`), of the uploaded template file, of a container template (`base-…`) and of a full clone. Free space of `plumb-images` (it is a dir on the host root fs?) and of `local-lvm`.
- **E2** Seconds: download in the controller, upload, `pct create` from archive, first start until `sshd` answers, full clone, template conversion.

## Out of scope
- Privileged containers. Nested Docker in LXC.

## Result (owner, max 8 lines)
branch · commit · tests · notes

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
- **D5** `fstrim`, `journalctl --vacuum-time`, `apt-get clean`, `/var/lib/systemd/random-seed`, `/var/lib/dbus/machine-id`: which exist and which fail in a container (the `seal` steps of D-205)?

### E. Space and time
- **E1** Space: data use of the rootfs volume of a fresh Debian container (`lvs`), of the uploaded template file, of a container template (`base-…`) and of a full clone. Free space of `plumb-images` (it is a dir on the host root fs?) and of `local-lvm`.
- **E2** Seconds: download in the controller, upload, `pct create` from archive, first start until `sshd` answers, full clone, template conversion.

## Plan (pl-architect, 2026-11-12) — DRAFT; waits for the host facts (`docs/tasks/T-038.log.md`), then review and the user's approval
Marks: **[H:Cn]** = this part depends on host answer Cn; the final plan replaces the mark with the fact or with the fallback named next to it.

### 0. Shape in one paragraph
An Image with `type: lxc` pins the URL and the SHA-256 of a Proxmox system template (like a VM Image, D-077). `FetchImage` downloads it into the controller, checks the checksum, appends the agent, its unit, the Plumb CA and `agent.json` to the tar stream, and uploads the result as `vztmpl` to `plumb-images`. `Create` makes an unprivileged container from that file (or a full clone of a built version) with the enrollment token in `env`. The agent reads the token from `/proc/1/environ`, enrolls, and the Run removes `env`. Then everything is the VM flow: Blocks, drift, Issues, delete with revoke.

### 1. Contract and data model (no migration)
- `Image.spec.type: lxc` exists. `url` must be `https://`; the docs and the UI help name the Proxmox URL and the SHA-256 of today's Debian 13 and Ubuntu templates (A1, A2). Plumb does not read the appliance index (D-224, question 1).
- `Target.spec.type: lxc` exists, and the Image type must match (`state/validate.go:702`, exists). New rules for `lxc`: `user` is refused ("an LXC Target has no bootstrap user; use the `deploy-user` Block"); `ssh_keys` go to `root` through Proxmox (D-226, question 2). `disk` is the root fs size (GB, min 2). `allow_restart` is accepted and has no effect yet (cores and memory are live, **[H:C5]**).
- `Image.spec.build` for `lxc`: allowed only with child card T-082 (question 3). Until then the validation refuses it with "LXC image builds come later".
- Contract text changes (`api/openapi.yaml`): the two "LXC: T-038" notes; the Target `type` description; `reported_ips` in a container (D-216 holds).

### 2. Template repack (`internal/driver/proxmox/ctpack`, pure Go, no Proxmox)
- Input: the downloaded file, already checked against the pinned SHA-256 (never parse before the check). Compression by magic bytes: zstd and gzip; anything else is refused with the name of the format **[H:A1]**. New dependency: `github.com/klauspost/compress/zstd`, decoder only (D-225).
- Output: `.tar.gz` (stdlib `compress/gzip`, fixed header, no time stamp), so Plumb needs no zstd encoder **[H:B3]**.
- **Byte-exact base:** the base entries are copied block by block up to the end of the last entry (the end is found with `archive/tar` over a counting reader); headers are never decoded and written again, so PAX, GNU long names, xattrs (`security.capability`), hard links, devices and sparse entries stay as they are **[H:A3]**. Then the Plumb entries are appended (USTAR, uid/gid 0, fixed mtime) and a new end-of-archive.
- Plumb entries: `/usr/local/bin/plumb-agent` (0755), `/etc/plumb-agent/ca.pem` (0644), `/etc/plumb-agent/agent.json` (0644: `url`, `ca`, `token_env: PLUMB_ENROLL_TOKEN`), `/etc/systemd/system/plumb-agent.service` (0644, the same unit as the vendor snippet) and the link `multi-user.target.wants/plumb-agent.service`.
- **Safety rules (refuse the base):** one of the Plumb paths exists in the base; a parent of a Plumb path is not a plain directory entry in the base (a symlink there would move our file on extraction); `..` or an absolute link target on a parent; more than 2 GiB unpacked tar or 200 000 entries (limits, so a wrong file cannot fill the disk); a truncated stream.
- Name: `plumb-ct-<image>-<d12>.tar.gz`, `d` = SHA-256 over (base SHA-256, agent binary SHA-256, CA, agent URL, pack format version). Same inputs → same name and the same bytes. A new agent or a new CA gives a new file; containers that exist keep their agent (agent self-update is later).

### 3. Driver (`internal/driver/proxmox`)
- The driver knows the type of a guest id from `GET /cluster/resources?type=vm` (`type: qemu|lxc`, needs `VM.Audit`); every method refuses a guest whose type differs from the spec. `GuestID` stays the VMID.
- `FetchImage(lxc)`: under the image lock, list `vztmpl` of `plumb-images`; a file of the name with the right size is reused. Else: download into `/var/lib/plumb/tmp` (space check first, D-219 style, against the free space of the controller fs **[H:A4]**), check SHA-256, repack to a second temp file, upload with `checksum` + `checksum-algorithm=sha256` **[H:B1]**, remove the temp files (also on failure). The `ImageRef` is the volid `plumb-images:vztmpl/<file>`.
- `Create(lxc)`: one `POST /nodes/<node>/lxc`: `ostemplate`, `vmid` (range), `pool`, `hostname`, `unprivileged=1`, `features=nesting=1` (systemd needs it **[H:D4]**), `rootfs=<disk storage>:<disk>`, `net0=name=eth0,bridge=…,ip=…,gw=…`, `nameserver`, `searchdomain` (D-187), `ssh-public-keys`, `cores`, `memory`, `swap=512`, `description` (image name and version, as VMs), `env=PLUMB_ENROLL_TOKEN=<token>` **[H:C1, C2]**; then start. A failure after the create removes the half-made container (D-078, T-073 rule).
- `Observe(lxc)`: config + pending + status + `interfaces` **[H:C7]**; `TokenPending` = `env` holds a token. A privileged container (`unprivileged` missing or 0) is an error "Plumb manages only unprivileged containers", never a change.
- `Update(lxc)`: `cores`, `memory`, the bridge of `net0` (live **[H:C5]**); a new token sets `env` (re-enrollment, then `Restart`). New `ClearBootstrap(id)`: `delete=env` with the digest; idempotent.
- `Power`, `Delete` (`purge=1`, `destroy-unreferenced-disks=1` **[H:C8]**), `DiskSpace` (also the image storage for the upload). `ListImages`/`RemoveImage` see `vztmpl` files of the Image; removal needs B4 — if the token gets 403, the rule of D-209 holds (report and show the `pvesm free` line, no new right).
- Create-only for `lxc` as for VMs (D-185 A): `ip`, `gateway`, `disk`, `ssh_keys`, `name`. A live `ip`/`disk` change of a container is a later card.

### 4. Engine (`internal/plan`)
- Create step: `FetchImage` → disk gate (D-219, root fs size; plus the template upload on the image storage) → `Create` → wait for the hello (as VMs) → **new step `guest:bootstrap` = `ClearBootstrap`** for `lxc`. A failed clear fails the Run with the text; the token is used and dead already, so this is hygiene, not a hole.
- Re-enrollment Plan for `lxc`: the same as VMs (new token in `env`, cold restart), then clear.
- Restart: `restart_needed` stays false for `lxc` while C5 shows live changes; if a change is pending, the D-190 path covers it unchanged.
- Delete: as VMs (D-211 revoke first).

### 5. Agent (`internal/agent`)
- `agent.json` gets `token_env`. With it the agent reads `/proc/1/environ` (NUL list, max 64 KiB, only the named key) instead of `token_file`; a missing key is "no token yet" (retry, as today). The value never reaches a log (existing rule).
- `symlink` (card note): `renameat2` with `EINVAL` (flags not supported, ZFS < 2.2 or another file system) gives "the file system of <dir> does not support atomic rename (RENAME_NOREPLACE/RENAME_EXCHANGE); on ZFS it needs ZFS 2.2 or newer". No fallback to `rename(2)`. A test with a seam that returns `EINVAL` (D-228).
- `seal` in a container (only with T-082): no cloud-init and no dracut steps; skip `fstrim` and what D5 shows as missing; machine-id and SSH host keys by the facts of D1.
- Addresses (D-216) are done; D3 confirms `eth0` is `veth` inside.

### 6. Security
- The template holds no secret: binary, public CA, URL. Anyone with `Datastore.Audit` on `plumb-images` can read it; that is fine.
- The token: single use, 15 min, stored as a hash, bound to the Target (D-025). It is visible in the container config to `VM.Audit` holders and in `/proc/1/environ` to root in the container until the next cold start **[H:C2, C3, C4]**. The clear step removes it from the config; a reader after the enrollment gets a dead token. Plumb never puts it into the description, a Change, an event or a log.
- Unprivileged only (D-227): create sets `unprivileged=1` and `nesting=1` only; no `lxc.*` raw keys, no mount points, no devices; Observe refuses a privileged container. Root in the container is uid 100000 on the host.
- Repack: parse only a checked file; the limits and parent rules of §2; temp files 0600 in a 0700 directory, removed on every path.
- The dependency: one module, decoder only, pinned in `go.sum`; `govulncheck` in CI covers it.
- New token rights: none planned. C1, C2 and C9 decide it; if a step needs a new right, the final plan names it and asks the user (D-081 changes only with the user).

### 7. Installer
- `plumb-images` gets content `import,vztmpl` on install and on a repair run (D-229); the start check of D-183 accepts exactly that set (and still `import` alone, with a warning that LXC Images need the repair run) **[H:G0]**.

### 8. Web
- Target form: type follows the Image (`vm`/`lxc`); for `lxc` no `user` field, `ssh_keys` labelled "SSH keys for root", no "Restart needed" badge. Target detail shows "Container". Image form: type `lxc` with the help text of §1; build section hidden for `lxc` until T-082. Screenshots with `browser-shot` (light and dark, 390 px).

### 9. Tests
- `ctpack`: fixture archives made in the test (zstd and gzip, with xattrs, hard link, device, long name, sparse); the base prefix of the output is byte-equal; GNU `tar -tv --xattrs` of the output lists base + Plumb entries (CI has GNU tar); refuse cases of §2 one by one; deterministic output.
- Driver: `pvefake` for the LXC endpoints; replay data recorded on the host (leak scan includes `env`); the half-made cleanup; type mismatch refused.
- Agent: `token_env` reader with a fake environ file (missing key, empty value, long value, NUL edge cases); symlink `EINVAL` seam.
- Engine: `driver/fake` LXC create, clear step, failure of the clear, re-enrollment, delete; leak test: no token in events, Plans, logs.
- Web: unit + e2e for the form rules.
- Mutation audits by `pl-codex-qa` as for T-039.

### 10. Real-host acceptance (T-083)
H1 Debian 13 container, static address, Block `nginx`: enrolled, first Run green, `env` gone from the config, Nginx answers. H2 the same with DHCP and with the Ubuntu template of A1. H3 drift: a file changed in the container → Issue in a few s; `pct set --memory` from outside → `guest:config` Issue. H4 live `cores`/`memory` change by a Plan, no restart. H5 re-enrollment. H6 delete: container gone, credential 401 (D-211). H7 second create reuses the template (no upload); a new agent build makes a new file; removal or the `pvesm free` line. H8 the `nginx-site` default link (renameat2 flags) in the container. H9 disk gate refuses an oversized root fs. H10 the token cannot create a privileged container or set `lxc.*` keys (exact 403 texts). End state clean.

### 11. Child cards (after the user's approval; numbers from T-075)
| Card | Owner | Size | Needs |
|---|---|---|---|
| T-075 contract + validation rules for `lxc` (§1), D-lines | pl-backend | S | plan |
| T-076 `ctpack` + the zstd dependency (§2) | pl-backend | M | plan (no host) |
| T-077 installer: `vztmpl` on `plumb-images`, start check (§7) | pl-codex-fix | S | plan |
| T-078 agent: `token_env`, `symlink` EINVAL text (§5) | pl-frontend | S | T-075 |
| T-079 driver LXC (§3) with host recordings | pl-backend | L | T-076, T-077 |
| T-080 engine: create/clear/re-enroll/delete for `lxc` (§4) | pl-frontend | M | T-075, fake driver |
| T-081 web (§8) | pl-frontend | S | T-075 |
| T-082 LXC image builds (container `seal`, template, clone) | pl-backend | M | T-079, T-080, question 3 |
| T-083 real-host acceptance (§10) + QA audits | pl-backend, pl-codex-qa | M | all |
Parallel: T-075, T-076, T-077 at once; then T-078, T-079, T-080, T-081.

### 12. Decisions (proposed; text in `docs/decisions/T-038.md`)
D-224 checksum source · D-225 zstd dependency · D-226 LXC bootstrap user and SSH keys · D-227 unprivileged + nesting only · D-228 renameat2 without fallback · D-229 `vztmpl` on `plumb-images` · D-230 repack format and name · D-231 token clear step.

### 13. Questions for the user (each with one recommendation)
1. **Checksum source of a base template.** Recommended: the Image pins URL + SHA-256 as for VMs; the docs list today's values. (Other: Plumb reads Proxmox's signed appliance index; needs an OpenPGP library and has only a SHA-512.)
2. **Login to a container.** Recommended: SSH keys go to `root` (Proxmox writes them at create); a named user comes from the `deploy-user` Block. (Other: the agent makes the Target's `user` at the first Run.)
3. **LXC image builds (Blocks baked into a container template).** Recommended: yes, as the last card T-082 after plain LXC Targets work. (Other: later slice.)
4. **Where LXC templates live.** Recommended: the existing storage `plumb-images` also takes `vztmpl` (installer repair run). (Other: a new storage `plumb-ct` with its own role.)

## Out of scope
- Privileged containers. Nested Docker in LXC.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-039 – Image builds: Blocks, seal, retention, schedule   (test host ready since 2026-11-11; needs the plan)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (will be split)   · **Depends on:** test host (D-041), T-012 and the agent PRs (#8, #13) on `main`

## Goal
Plumb builds its own templates from a base image plus Blocks, keeps versions, removes old ones and rebuilds on a schedule.

## Design (decision level – D-098, BRIEF §5.4, ARCHITECTURE §12)
- `Image` spec gets `blocks`, `retention.keep`, `schedule`.
- Build = Run: temporary guest → apply Blocks → agent `seal` → stop → template → image version row.
- A build never changes a target. A target records its image version.
- Blocks of an Image cannot use secret references. `seal` also removes the agent's desired list, so a template holds no secret.
- Retention keeps the newest `keep` versions and every version that a guest still needs.
- A scheduled build is planned by `system`. Its approval rule waits for the user's answer (T-025, question 2).

## Before this card starts
- [ ] `pl-architect` turns the design into a plan with the facts from the host: template conversion, linked and full clones per storage, what `seal` must reset on Debian 13 and Ubuntu 26.04, delete rules for templates with clones.
- [ ] Parts that need no host (schema, version table, retention choice, schedule clock) can become their own unblocked card then.
- [ ] Needs new architecture decisions.
- [ ] Facts from the real host that the plan must cover (pl-lead, 2026-11-11):
  - D-192: `seal` (or a Block) removes initramfs networking on dracut images (Ubuntu 26.04), so a Plumb-built Ubuntu template takes a static address. Proof on the host: static address applied at the first and at a later boot.
  - D-185 / pl-architect: an image build must remove the SSH host keys before the template is made.
  - D-188 (T-055): there is no Image binding; a template is found by name + full SHA-256 in the pool. Templates and import files are never deleted by the driver (D-077): the retention rule of this card decides who deletes them.

## Host questions (pl-architect, 2026-11-11; pl-backend answers them on `pve-dev`)
The plan needs these facts. Write the answers to `docs/tasks/T-039.log.md`: one row per id (`PASS`/`FAIL`/value, time, exact error text). Redact addresses, MACs and token ids. No code change, no installer run.

**Known, do not test again:** the disk storage is `local-lvm` (lvmthin, about 42 GB); the driver always makes a full clone (`full=1`, `driver.go:512`); a clone does not lock its template (T-055 item 3); `seal` exists only in the docs, not in the agent; the D-192 diagnosis (T-015 log).

**Allowed on the host:** read-only commands; test VMs and templates in the pool and the VMID range, made by Plumb or by root `qm`; SSH into test guests with the guest test key (host as jump); root `qm set|shutdown|template|clone|destroy` on these test VMs only, with the guard of T-055 (in range, in the pool, exact name); `pvesm free` for import files of this card by exact name. Not allowed: a new storage, a new right for the token (report the missing right instead), anything from the "never" list of AGENTS.md. End state: 0 guests, 0 templates, 0 import files of this card.

**Method.** Per distribution (Debian 13, Ubuntu 26.04; the pinned images of T-015): Plumb makes build guest `B` (DHCP, 10 GB, Block `nginx`) through the API. Run the seal script below in `B` by SSH. Shut down, clear the config (A2), convert. For the clones use the adoption rule of D-188, so the real driver and the real enrollment run: destroy the stock template, give the sealed template the name `plumb-img-<image>-<sha12>` and the description line `sha256=<full>` of an Image document, then let Plumb make Target `C1` (static, `PLUMB_TEST_VM_IP_1`) and `C2` (DHCP) from it, both with the Block `nginx` and a bootstrap user with a key.

Seal script, candidate (root, in this order; report every step that fails or does not exist):
```sh
systemctl disable --now plumb-agent.service
rm -rf /var/lib/plumb-agent /etc/plumb-agent /usr/local/bin/plumb-agent /usr/local/sbin/plumb-agent-install /etc/systemd/system/plumb-agent.service
# Ubuntu only, D-192 (see D1 … D3)
rm -f /etc/cloud/cloud-init.disabled
cloud-init clean --logs --seed --machine-id --configs all
rm -f /etc/ssh/ssh_host_*
[ -L /var/lib/dbus/machine-id ] || rm -f /var/lib/dbus/machine-id
rm -f /var/lib/systemd/random-seed /var/lib/systemd/credential.secret
userdel -r <bootstrap user of B>; rm -f /etc/sudoers.d/90-cloud-init-users
apt-get clean; journalctl --rotate; journalctl --vacuum-time=1s; fstrim -av
```

### A. Template conversion of a guest that Plumb made
- **A1** With the Plumb token: `POST …/qemu/<B>/template` on the stopped `B` (it ran, it has a cloud-init drive and a grown disk). Works? Time? Volumes after it (`base-…`, cloud-init volume).
- **A2** Print the config of `B` before the conversion (serial redacted). Can the token remove the build values in one write with the digest: `delete=sshkeys,ciuser,ipconfig0,cicustom,nameserver,searchdomain,ciupgrade,onboot`, `smbios1` without the serial, a new `description`? Which key is refused? Can the token change `name` and `description` of a template after the conversion?
- **A3** In a full clone of the template: which values does Proxmox make new (`smbios1` uuid, `vmgenid`, MAC of `net0`) and which does the clone take over? Is any value of `B` left in `C1` after the driver's `configure` (look at `sshkeys` when the Target has no key)?
- **A4** Disk: the template has 10 GB. A Target with `disk: 10` (resize to the same size: error or no-op?), with `disk: 20` (root file system grown at first boot, both distributions?), with `disk: 8` (exact error text of the driver).
- **A5** Space: `lvs` data usage of the template volume and of one full clone, per distribution.

### B. Clones and delete
- **B1** lvmthin: time of a full clone and of a linked clone (`qm clone … --full 0`, root, test VM) of the sealed template. The host has no other disk storage type; say so if that is wrong.
- **B2** Delete of a template: (a) with a full clone alive: allowed? the clone still starts? (b) with a linked clone alive: exact error text. (c) With the Plumb token: `DELETE …/qemu/<template>` works? Which parameters (`purge`, `destroy-unreferenced-disks`)? Are the base disk and the cloud-init volume gone?
- **B3** With the Plumb token: `DELETE …/storage/plumb-images/content/<import volid>`. Works with the rights of today? If not: the exact error and the right that Proxmox names.
- **B4** Read-only: VMID range and free ids of the running controller, free space of `local-lvm`. How many 10 GB templates plus guests fit?

### C. Seal: what it must reset, and the proof (both distributions)
- **C0** Before seal, read-only in `B`: `cloud-init --version`; `cloud-init clean --help` (do `--machine-id`, `--seed`, `--configs` exist?); `ls -l /etc/machine-id /var/lib/dbus/machine-id`; the network files that cloud-init wrote (`/etc/netplan`, `/etc/network/interfaces.d`); what cloud-init left for the bootstrap user (home, `authorized_keys`, sudoers file); `dpkg -l dracut* initramfs-tools`.
- **C1** After the seal script: does `B` shut down clean? Does a clone boot? (A cold boot with `/etc/machine-id` = `uninitialized` and no SSH host keys.)
- **C2** machine-id: three different values in `B` (before seal), `C1`, `C2`; none empty or `uninitialized`. `C2` and a second DHCP clone get different leases.
- **C3** SSH host keys (D-185): fingerprints of `B`, `C1`, `C2` all differ; `sshd` runs; the keys stay after `qm reboot` and after a cold stop and start.
- **C4** cloud-init: `C1` and `C2` ran as a new instance (one directory in `/var/lib/cloud/instances`, instance-id is not the one of `B`), hostname = Target name, the Target's bootstrap user and key exist, the user of `B` does not; after enrollment `cloud-init.disabled` is back.
- **C5** Agent identity: the Run of `C1` succeeded with a new agent; `/var/lib/plumb-agent` holds only the new credential and the new desired list; the agent of `B` is not `online` again. The Block result is in the image: the step `package:nginx` is `unchanged` in the first Run of `C1`, and Nginx answers at first boot.
- **C6** Logs: `journalctl --list-boots` in `C1` shows no boot of `B`; `/var/log/cloud-init.log` starts with this boot; no file under `/var/log`, `/root`, `/home`, `/tmp` holds the hostname, the address or the user of `B` (`grep -rl`).
- **C7** What is left that the script did not reset? Look at `/var/lib/dhcp`, `/var/lib/systemd`, `/etc/hostname`, `/etc/hosts`, `/var/lib/apt/lists`, shell histories. List it; do not fix it.

### D. Ubuntu 26.04: no network setup in the initramfs (D-192)
- **D1** Read-only on a stock Ubuntu guest: `dpkg -l | grep -i dracut`; what needs `dracut-network` (`apt-cache rdepends --installed dracut-network`, `apt-mark showmanual | grep dracut`); `lsinitrd | grep -i -E 'network|dhcp'`; `/proc/cmdline`; which file makes `zzzz-dracut-default.network`.
- **D2** Fix candidate 1, in `B` before seal: `/etc/dracut.conf.d/90-plumb-no-network.conf` with `omit_dracutmodules+=" network systemd-networkd "` (use the module names that D1 shows), then `dracut -f --regenerate-all`. If it does not help, candidate 2: `apt-get -y remove dracut-network` and regenerate. Report which one works and what `apt` wants to remove with it.
- **D3** Proof on `C1` (static): at the **first boot** the configured address answers (neighbor table of the controller LXC, as in T-015), the NIC name and its network file (`networkctl status`), time until the agent is online; the same address after `qm reboot` and after a cold stop and start. `C2` (DHCP) still gets an address; time of its first boot (stock: about 3 min).
- **D4** Does the fix stay after a kernel update in a clone (`apt-get -y install --reinstall linux-image-$(uname -r)` or the next kernel, then `lsinitrd` again)?
- **D5** Debian 13: is the candidate a no-op there (no dracut)? `C1` on Debian takes the static address at first boot as before?

### E. Time and size of a build
- **E1** Seconds per phase, per distribution: `FetchImage` (template there / not there), clone, first boot until the agent is online, apply of `nginx`, seal script, shutdown, conversion. Sum.
- **E2** `apt-get update && DEBIAN_FRONTEND=noninteractive apt-get -y full-upgrade` in a fresh guest from today's stock image: time, number of packages, new kernel yes/no, extra disk use. (The plan decides if a build upgrades the packages, D-186.)

## Out of scope
- Signed image checksums. Images for other distributions.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# Release notes: v0.1.0

The text below the line is the final text for the draft release `v0.1.0` (release workflow run 44962338394). pl-lead copies it into the draft release; the user publishes it. Sources: `CHANGELOG.md`, the host runs T-263, T-277, T-279 and T-280, the scans of T-266, `docs/plans/finished-check.md` and the CI runs of the probe loop.

Open before use:

- **Expected for commit `a9d1e021eabf` with version `v0.1.0`** (RC6's code `3eab710` plus no build input; the local build T-279 R6-0; the draft release's `SHA256SUMS` equals it):
  - `plumb` `0856d9e34394582b72f8260ce1207aaa8bce53d6354a747b98efafe688f83416`
  - `plumb-agent` `ccc1f0f50add6d380a9d683161dddf69f90796e3a9361bb45d998f113112bf6e`
  - `install.sh` `facb822289e47983ce03bc46ef10539a228526b9a6ae03dc3e6110203995f3fe`
  - `plumb-v0.1.0-linux-amd64.tar.gz` `d584f47a7528c0ef82aa6da60031b2891aac5099da7a9aafae991db149c4b25c`

---

# Plumb v0.1.0

The first release of Plumb.

Plumb is below 1.0: its API and its file formats may still change between releases.

## Install or upgrade

Download `plumb-v0.1.0-linux-amd64.tar.gz` and `SHA256SUMS` from this page. Check them **before** you unpack:

1. **Who built it:** `gh attestation verify plumb-v0.1.0-linux-amd64.tar.gz --repo Bernotese/plumb --signer-workflow Bernotese/plumb/.github/workflows/release.yml --source-ref refs/heads/main`. This shows that the release workflow of this repository built the file from the commit that it names.
   - It needs `gh` 2.102.0 or newer (`gh --version`). An older `gh` checks the two last options too loosely, and the `gh` of most distributions has no `gh attestation` at all.
   - Get a current one from https://cli.github.com or https://github.com/cli/cli/releases.
2. **The file is whole:** `sha256sum -c SHA256SUMS`.

Then unpack it as root into a folder that only root can write, and run its `install.sh` from there, first with `--dry-run` (see the README, "Install"). The installer checks the inner `SHA256SUMS` before it uses a file. That proves only that the files are whole, not who made them: step 1 does that.

**Upgrade:** there is no release before this one.

- **From a build from source:** run this release's `install.sh` with the same options as before (a repair run), `--dry-run` first.
- **Name every option again:** every extra bridge, VLAN tag and guest backup storage. A repair run removes the rights of the ones it is not given.
- **Agents:** they do not update by themselves. After the upgrade the agent page of each Target shows its state: "Update available" or "Update required" (Plumb refuses the Plans of that Target until the update) → propose and approve one agent update Plan per Target; "Manual update" → the one root command below.
- **Old guests:** a guest made before the agent could update itself needs one root command, once (README, "Guests made before the agent self-update").
- **Container Images built before this release:** use **Build now** once on each such Image before you make new containers from it. A container from an older built version could miss its agent at its first boot: its create Run ends with "the agent of … did not come online", and its agent page says "no agent yet". The README, "Updates and repair", shows how to fix such a container. A stock container Image needs nothing: Plumb packs its template again at its next use.

## What is new

Everything; the README describes each feature.

- **Install:** `install.sh` puts Plumb into its own container (LXC) on one Proxmox host, with a dry run first. It makes the Proxmox user, its token and exactly the rights of the README's rights table, and shows a recovery key once. A release tarball is checked against its `SHA256SUMS` before any file is used.
- **Images:**
  - Debian 13 and Ubuntu 26.04 cloud images and container templates, checked against their published checksums (signed ones where the vendor signs them).
  - Built Image versions on a schedule, with a fixed number kept.
  - Deleting an Image removes the templates that Plumb made for it, and names the downloaded files that stay.
- **Targets:**
  - VMs and containers made from an Image: cores, memory, the main disk, and an optional power state.
  - Up to 3 data disks in fixed slots (a disk only grows).
  - Up to 4 network cards, on the bridges and VLAN tags that the install names.
  - Adopt a guest that Plumb did not make.
- **Blocks:** a starter catalogue (for example `deploy-user`, `ssh-hardening`, `nginx`, `docker-engine`, `firewall`, `network`) and your own Blocks, with a preview of the parameter form and the resources while you edit.
- **Plans and Runs:** every change is a Plan first, with each change and its risk; nothing changes until it is approved. The history keeps every revision.
- **Issues:** drift of a guest opens an Issue: revert, accept or ignore it. Hand changes of the network and the firewall are seen within seconds.
- **Agents:** an agent in each guest applies the Blocks and reports drift. It updates itself through a Plan, and it goes back to its old version when Plumb is not reachable right after the update.
- **Backups and restore:**
  - Plumb's own encrypted backups, a copy off the host, and the restore of a whole controller.
  - Guest backups on a storage that the install names, on a schedule or on demand.
  - The restore of a guest, with a safety backup first.
- **Users and access:** users with the roles read, plan, apply and admin; API tokens with a tier and a highest risk; export and import of all documents.
- **AI clients (MCP):** `plumb mcp` serves an MCP client with tools and the prompt "Explain an Issue". The client works with the rights of its token: with a `plan` token it can comment and propose Plans, and approving stays with a person.

## Proven on a real Proxmox host

Before this release, its release candidates went through full runs on a real Proxmox VE host:

- each release candidate was installed from its release tarball, as a user gets it, and the last one is the code of this release;
- every run of the installer had its dry run first, and the real run did exactly what the dry run listed.

- **Install from the tarball:** the installer refuses a changed file in the bundle and an unsafe `TMPDIR`, and changes nothing when it refuses.
- **Five upgrades, each from one release candidate to the next:**
  - no Proxmox right changed: the ACL lines were equal before and after, and the run had no `pveum` line;
  - no database table lost a row;
  - the storage config stayed byte-equal;
  - where guests ran, every guest stayed ready and online, and each agent then took its update Plan.
- **The features:**
  - a VM and a container from an Image, with Blocks; containers from stock and from built Images, each with one enrollment at its first boot and none at a reboot;
  - the web UI on the real controller with the roles read and admin, at desktop and phone width and in dark mode, with no console error (the login page shows only the expected 401 of its session check);
  - drift and its revert; ignore by rule (the same drift opens no Issue while the rule exists, and one again after it goes); accept as raw;
  - the rollback to an old revision, and the import of a changed Block (a Plan that names only that Block);
  - a timer unit, and a `command` resource that runs once;
  - the Blocks `shell` and `common-tools` in a VM and a container;
  - a new Image version leaves the Targets of the old version alone;
  - the guard of the agent's own files, also through a relative symbolic link;
  - a network card on an extra bridge with a VLAN tag, and the firewall;
  - data disks, added and grown, with their data kept;
  - a guest backup and its restore;
  - the restore from a damaged backup, which fails and names the safety backup as the way back;
  - "Explain an Issue" over MCP with a `plan` token (it cannot approve);
  - the adoption of a guest and the repair of its agent.
- **The restore of Plumb itself** (remove the controller and keep the guests, then a clean install with the restore of a backup):
  1. every Target reconciles, and no drift Issue opened in 15 minutes;
  2. every agent is online without a re-enrollment, and the SSH host keys stay pinned;
  3. the admin logs in with the password that the restore showed and must set a new one first (after it only the new one works), the tokens work within their tier once a person enables them again, and a revoked token stays revoked. The other users are locked by the restore. A person unlocks each one with a one-time password: the user must set a new password before anything else, and the used one-time password is refused;
  4. the guest backups are still listed, and a guest with a data disk restores from one;
  5. the guests, their data disks and their network cards are unchanged;
  6. the Proxmox rights and the storage config are equal;
  7. no secret leaked: a scan of the release tarballs and of what each controller wrote (its database, its logs, its events and the installer output) found no token, password or key.

**Proven by the automated tests only, not on a host:**

- the encryption of guest backups on a Proxmox Backup Server (no such server was at hand; see "Known limits");
- that a hand change counts as drift only for a resource that a Block manages, next to managed files in the same guest (the host showed only a guest without managed resources);
- the rate limit of drift reports;
- the network match by the permanent MAC address (in CI only);
- a newer signed pin of an Image, end to end;
- the refusal that keeps the last admin (a host cannot reach it in the order of the API calls).

## Known limits

- **A test that failed without a known cause:** the firewall test of the CI (`TestGuestFirewall`) failed twice: once before Plumb's firewall code ran, and once when a port that Plumb's table accepts was not reachable. The cause was not found.
  - Since then, a loop runs in the guest CI jobs (first after the boot in the firewall job). It builds the same network for each round and probes it once: in 96 CI runs from 2026-11-19 to 2026-11-21, **56800 of 56800 rounds connected, 0 failed**.
  - The cause stays unexplained. A new failure stops the loop with a dump of the network state.
- **A new network card makes the guest ask DHCP again on its main card:** when Plumb adds or changes a network card, the guest asks its DHCP server again for the address of its main card. In the tests it kept the same address, with no gap (under 20 ms). If your DHCP server may hand out another address, give the main card a static address or a DHCP reservation.
- **A stock Ubuntu 26.04 image** used directly by a Target can stay on DHCP until its first restart. Use DHCP, or let Plumb build the Image: a built Image does not have this limit.
- **Proxmox Backup Server:** Plumb reads from the storage config whether a backup storage encrypts. This was not tested on a real Proxmox Backup Server.
- **A gone guest:** Plumb restores a guest backup only over the same guest, not after the guest is gone. Restore it in Proxmox into the pool `plumb`, then adopt the guest.
- One Proxmox host only; no clusters. The README, "Known limits", has the rest.

## Rights

The Proxmox user `plumb@pve` and its token `plumb@pve!plumb` get the roles of the README's rights table, and nothing else:

- `PlumbGuests` on the pool;
- `PlumbDisk`, `PlumbImages` and `PlumbSnippets` on their storages;
- `PlumbBridge` on each named bridge or bridge/VLAN tag;
- `PlumbNode` on the node;
- `PlumbAudit` (`Sys.Audit`, not inherited) on `/`.

Guest backups (`--guest-backup-storage`) add `PlumbGuestBackupVM` on the pool and `PlumbGuestBackup` on that storage. More networks (`--extra-bridge`, `--extra-vlan`) add one `PlumbBridge` line each. These lines come only when you name the option.

Plumb never gets root access to Proxmox. This is the first release, so no right changed since a version before.

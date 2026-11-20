# Release notes: draft for the first release

A **draft** made from `docs/RELEASE-NOTES-TEMPLATE.md`, `CHANGELOG.md` ("Unreleased") and the host re-run T-263. It is not a release: no tag and no GitHub release exist. **The version and the date stay placeholders until the user decides them (U6).** pl-lead copies the text below the line into the draft release that the release workflow makes, checks each line against `CHANGELOG.md` and `docs/STATUS.md`, and the user publishes it.

Open before use:

- Fill `<version>` and `<date>`.
- **Expected for commit `3eab710` with version `0.1.0`** (a local build, T-279 R6-0; a commit that changes a build input changes the hashes):
  - `plumb` `0856d9e34394582b72f8260ce1207aaa8bce53d6354a747b98efafe688f83416`
  - `plumb-agent` `ccc1f0f50add6d380a9d683161dddf69f90796e3a9361bb45d998f113112bf6e`
  - `install.sh` `facb822289e47983ce03bc46ef10539a228526b9a6ae03dc3e6110203995f3fe`
  - `plumb-v0.1.0-linux-amd64.tar.gz` `d584f47a7528c0ef82aa6da60031b2891aac5099da7a9aafae991db149c4b25c`

  The draft release's `SHA256SUMS` must equal these when it is built from that commit.

- Keep step 1 of "Install or upgrade" only if the release has a build attestation (the release workflow of PR #392).
- Update the O1 line with the newest count of the CI loop.
- Add the C5b scan result of T-263 to "Proven on a real Proxmox host" when pl-reviewer has reported.

---

# Plumb <version>

The first release of Plumb. <date>

Plumb is below 1.0: its API and its file formats may still change between releases.

## Install or upgrade

Download `plumb-<version>-linux-amd64.tar.gz` and `SHA256SUMS` from this page. Check them **before** you unpack:

1. **Who built it:** `gh attestation verify plumb-<version>-linux-amd64.tar.gz --repo Bernotese/plumb --signer-workflow Bernotese/plumb/.github/workflows/release.yml --source-ref refs/heads/main`. This shows that the release workflow of this repository built the file from the commit that it names.
   - It needs `gh` 2.102.0 or newer (`gh --version`). An older `gh` checks the two last options too loosely, and the `gh` of most distributions has no `gh attestation` at all.
   - Get a current one from https://cli.github.com or https://github.com/cli/cli/releases.
2. **The file is whole:** `sha256sum -c SHA256SUMS`.

Then unpack it as root into a folder that only root can write, and run its `install.sh` from there, first with `--dry-run` (see the README, "Install"). The installer checks the inner `SHA256SUMS` before it uses a file. That proves only that the files are whole, not who made them: step 1 does that.

**Upgrade:** there is no release before this one.

- **From a build from source:** run this release's `install.sh` with the same options as before (a repair run), `--dry-run` first.
- **Name every option again:** every extra bridge, VLAN tag and guest backup storage. A repair run removes the rights of the ones it is not given.
- **Agents:** they do not update by themselves. After the upgrade the agent page of each Target shows its state: "Update available" or "Update required" (Plumb refuses the Plans of that Target until the update) → propose and approve one agent update Plan per Target; "Manual update" → the one root command below.
- **Old guests:** a guest made before the agent could update itself needs one root command, once (README, "Guests made before the agent self-update").

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

Before this release, its release candidates went through a full run on a real Proxmox VE host:

- each release candidate was built from the release tarball, as a user gets it;
- every run of the installer had its dry run first, and the real run did exactly what the dry run listed.

- **Install from the tarball:** the installer refuses a changed file in the bundle and an unsafe `TMPDIR`, and changes nothing when it refuses.
- **Upgrade from one release candidate to the next:**
  - no Proxmox right changed: the ACL lines were equal before and after, and the run had no `pveum` line;
  - no database table lost a row;
  - the storage config stayed byte-equal;
  - every guest stayed ready and online;
  - each agent then took its update Plan.
- **The features, one Target of each kind:**
  - a VM and a container from an Image, with Blocks;
  - drift and its revert;
  - a network card on an extra bridge with a VLAN tag, and the firewall;
  - data disks, added and grown, with their data kept;
  - a guest backup and its restore;
  - the restore from a damaged backup, which fails and names the safety backup as the way back;
  - "Explain an Issue" over MCP with a `plan` token (it cannot approve);
  - the adoption of a guest and the repair of its agent.
- **The restore of Plumb itself** (remove the controller and keep the guests, then a clean install with the restore of a backup):
  1. every Target reconciles, and no drift Issue opened in 15 minutes;
  2. every agent is online without a re-enrollment, and the SSH host keys stay pinned;
  3. the admin logs in with the password that the restore showed and must set a new one first (after it only the new one works), the tokens work within their tier once a person enables them again, and a revoked token stays revoked. The other users are locked by the restore; their unlock by a one-time password was not run on the host (it is covered by the automated tests only);
  4. the guest backups are still listed, and a guest with a data disk restores from one;
  5. the guests, their data disks and their network cards are unchanged;
  6. the Proxmox rights and the storage config are equal;
  7. <the result of the secret scan of the new controller's data and logs>.

## Known limits

- **A test that failed without a known cause:** the firewall test of the CI (`TestGuestFirewall`) failed twice in an early step, before Plumb's firewall code ran. The cause was not found.
  - Since then, a loop runs in the guest CI jobs (first after the boot in the firewall job). It builds the same network for each round and probes it once: in 7 CI runs, **4000 of 4000 rounds connected, 0 failed** (status of 2026-11-19).
  - The cause stays unexplained. A new failure stops the loop with a dump of the network state.
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

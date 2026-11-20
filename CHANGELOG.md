# Changelog

Every release of Plumb gets one section here, newest first. A section names the version and the date, then:

- **Added**, **Changed**, **Fixed**, **Removed**: what a user notices, in short lines.
- **Rights:** the Proxmox token rights that the release needs, and every right that changed (an upgrade needs a repair run of the installer then).
- **Upgrade:** what to do from the version before (for example a repair run, or a step per guest).
- **Known issues:** what does not work yet, or was not tested.

How to check a download: `docs/RELEASE.md`.

## Unreleased

The first release. Its version and date are not decided yet (the proposal is 0.1.0). Plumb is below 1.0: its API and its file formats may still change between releases. The README describes each feature.

### Added

- **Install:** `install.sh` puts Plumb into its own container (LXC) on one Proxmox host, with a dry run first. It makes the Proxmox user, its token and exactly the rights of the README's rights table, and shows a recovery key once. A release tarball is checked against its `SHA256SUMS` before any file is used.
- **Images:** Debian 13 and Ubuntu 26.04 cloud images and container templates, checked against their published checksums (signed ones where the vendor signs them). Built Image versions on a schedule, with a fixed number kept. Deleting an Image removes the templates that Plumb made for it and names the downloaded files that stay.
- **Targets:** VMs and containers made from an Image: cores, memory, the main disk, up to 3 data disks in fixed slots (a disk only grows), up to 4 network cards on the bridges and VLAN tags that the install names, and an optional power state. Adopt a guest that Plumb did not make.
- **Blocks:** a starter catalogue (for example `deploy-user`, `ssh-hardening`, `nginx`, `docker-engine`, `firewall`, `network`) and your own Blocks, with a preview of the parameter form and the resources while you edit.
- **Plans and Runs:** every change is a Plan first, with each change and its risk; nothing changes until it is approved. The history keeps every revision.
- **Issues:** drift of a guest opens an Issue: revert, accept or ignore it. Hand changes of the network and the firewall are seen within seconds.
- **Agents:** an agent in each guest applies the Blocks and reports drift. It updates itself through a Plan, and it goes back to its old version when Plumb is not reachable right after the update.
- **Backups and restore:** Plumb's own encrypted backups, a copy off the host, and the restore of a whole controller. Guest backups on a storage that the install names, on a schedule or on demand, and the restore of a guest with a safety backup first.
- **Users and access:** users with the roles read, plan, apply and admin; API tokens with a tier and a highest risk; export and import of all documents.
- **AI clients (MCP):** `plumb mcp` serves an MCP client with tools and the prompt "Explain an Issue". The client works with the rights of its token: with a `plan` token it can comment and propose Plans, and approving stays with a person.

### Rights

The Proxmox user `plumb@pve` and its token `plumb@pve!plumb` get the roles of the README's rights table. Guest backups (`--guest-backup-storage`) and more networks (`--extra-bridge`, `--extra-vlan`) add their lines only when you name them. Plumb never gets root access to Proxmox.

### Upgrade

- There is no release before this one. To move an install that was built from source to this release, run the release's `install.sh` with the same options as before (a repair run), `--dry-run` first. Name every extra bridge, VLAN tag and guest backup storage again: a repair run removes the rights of the ones it is not given.
- Guests made before the agent could update itself need one root command each (README, "Guests made before the agent self-update").
- Container templates: the first-boot install of the agent checks the agent's binary now. Before, a container could miss its agent at its first boot when the two units started in one order. Plumb packs each stock container template again the next time it is used; an old file that no container uses any more then shows as unused on the Image page, with the command that removes it. A built container Image keeps the old files in its versions: use "Build now" once before you make new containers from it (README, "Updates and repair").

### Known issues

- A stock Ubuntu 26.04 image used directly by a Target can stay on DHCP until its first restart. Use DHCP, or let Plumb build the Image.
- Proxmox Backup Server: Plumb reads from the storage config whether a backup storage encrypts. This was not tested on a real Proxmox Backup Server.
- Plumb restores a guest backup only over the same guest, not after the guest is gone. Restore it in Proxmox into the pool `plumb`, then adopt the guest.
- The README, "Known limits", has the rest.

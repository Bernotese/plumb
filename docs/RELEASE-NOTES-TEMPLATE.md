# Release notes template

pl-lead copies this into the draft release that the release workflow makes, fills in the parts in `<…>`, and checks every line against `CHANGELOG.md` and `docs/STATUS.md`. **Nothing here is published by an agent: the user reads the draft and publishes it.** The text below the line is the release text.

---

# Plumb <version>

<One line: what this release is. For the first release: "The first release of Plumb.">

Plumb is below 1.0: its API and its file formats may still change between releases.

## Install or upgrade

Download `plumb-<version>-linux-amd64.tar.gz` and `SHA256SUMS` from this page. Check them **before** you unpack:

1. Who built it: `gh attestation verify plumb-<version>-linux-amd64.tar.gz --repo Bernotese/plumb --signer-workflow Bernotese/plumb/.github/workflows/release.yml --source-ref refs/heads/main`. This shows that the release workflow of this repository built the file from the commit that it names. <Leave this step out if the release has no attestation.>
2. The file is whole: `sha256sum -c SHA256SUMS`.

Then unpack it as root into a folder that only root can write, and run its `install.sh` from there, first with `--dry-run` (see the README, "Install"). The installer checks the inner `SHA256SUMS` before it uses a file. That proves only that the files are whole, not who made them: step 1 does that.

<Upgrade: what to run from the version before, for example a repair run with every extra bridge and VLAN that the install has now. Name every right that changed.>

## What is new

<From `CHANGELOG.md`: Added, Changed, Fixed, Removed.>

## Known limits

- **Proxmox Backup Server:** Plumb reads from the storage config whether a backup storage encrypts. This was not tested on a real Proxmox Backup Server.
- **A stock Ubuntu 26.04 image** used directly by a Target can stay on DHCP until its first restart. Use DHCP, or let Plumb build the Image: a built Image does not have this limit.
- **Dependencies:** `npm audit` reports one low advisory (GHSA-gcqx-w48m-w2h4, `deepmerge` 4.3.1). It comes only with the build tool of the web UI, it is not in the shipped files, and no fixed version exists yet.
- **A test that failed without a known cause:** the firewall test of the CI (`TestGuestFirewall`) failed twice in an early step, before Plumb's firewall code ran. The cause was not found. <Update from the QA sweep: still unexplained, or the cause and its fix.>
- <Any other line of "Known issues" in `CHANGELOG.md`.>

## Rights

<The Proxmox token rights of this release (README, "Rights"), and what changed since the version before.>

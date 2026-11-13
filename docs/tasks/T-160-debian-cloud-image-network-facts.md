# T-160 – Facts: network setup of the real Debian 13 cloud image

- **Owner:** pl-backend   · **Reviewer:** pl-architect (uses it in T-145)   · **QA:** –
- **Size:** S (facts only)   · **Depends on:** –

## Goal
T-149 measured an LXD image for Debian (`images:debian/13/cloud`: no netplan, systemd-networkd). Plumb builds Debian VMs from the vendor cloud image (the one the Image documents pin). The contract of `network` (T-145) needs the facts of that image.

## Context (read only these)
- `docs/plans/netplan-probe.md` on branch `pl-codex-qa/T-149-netplan-probe` (PR #242)
- The Debian 13 image URL that the demo/starter Image documents use

## Acceptance criteria
- [ ] Without the host first: the package list that Debian publishes next to the image (manifest/JSON), or the image itself inspected on the dev VM: is `netplan.io` in it, which renderer, which file does cloud-init write (`50-cloud-init.yaml` or a networkd file), how is the NIC matched (name or MAC).
- [ ] Only if that leaves a doubt: one Debian 13 VM on `pve-dev` through Plumb (normal Target in the pool, documentation-range extra data not needed), read the same facts in the guest, run one unchanged `netplan apply` (or the networkd reload) and say if the agent link dropped; delete the Target and its image objects at the end. Nothing else on the host.
- [ ] `docs/plans/debian-network-facts.md` on your branch (≤ 40 lines, no address/MAC), pushed, no PR. Report to `pl-lead`.

## Out of scope
- Code. Any change of rights, network or storage on the host.

## Result (owner, max 8 lines)
branch · commit · tests · notes

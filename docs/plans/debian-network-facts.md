# Debian 13 cloud image: network facts (T-160)

Image: `debian-13-genericcloud-amd64`, our pin `20261103-2651` (also checked `20261111-2659`, same versions).
Sources: the Debian manifest (`.json` next to the image), the image root file system read on the dev VM, Proxmox `qemu-server` `Cloudinit.pm`, and one Plumb VM on `pve-dev` (2026-11-13, controller `71c14c0`, static address).

## Static facts (manifest + image)

- `netplan.io`, `netplan-generator`, `python3-netplan` 1.1.2-7 are installed. cloud-init 25.1.4-1+deb13u1, systemd 257.13.
- No `ifupdown`, no NetworkManager. `systemd-networkd` (+ socket, wait-online) and `systemd-resolved` are enabled. `dhcpcd-base` is only a library/tool, no service.
- `/etc/netplan` and `/etc/systemd/network` are empty in the image. No network section in `/etc/cloud/cloud.cfg*`.
- cloud-init renderer: the default order is `eni, sysconfig, netplan, …`. `eni` needs `ifup`, so cloud-init selects **netplan** (guest log: `Selected renderer 'netplan'`).
- No `renderer:` key in the file, so netplan uses its default **networkd** (generator → `/run/systemd/network`).

## What a Plumb VM gets (Proxmox NoCloud, `ipconfig0`)

- Proxmox writes network-config **v1**: `type: physical`, `name: eth0`, `mac_address`, one subnet (static or `dhcp4`). DNS (`nameserver`/`searchdomain`) is a global `type: nameserver` entry.
- cloud-init writes **`/etc/netplan/50-cloud-init.yaml`** (mode 0600, through the netplan Python API):
  `ethernets.eth0: match.macaddress, set-name: eth0, addresses, routes [to: default, via], nameservers {addresses, search}`.
- The NIC is matched by **MAC**, and renamed to `eth0` with `set-name`.
- Generated: `/run/systemd/network/10-netplan-eth0.link` (`PermanentMACAddress=`, `Name=eth0`) and `10-netplan-eth0.network` (`PermanentMACAddress=` + `Name=eth0`, `Address=`, `DNS=`, `Domains=`, `[Route] Gateway=`, `LinkLocalAddressing=ipv6`).
- Kernel cmdline has no `net.ifnames=0`. The `.link` file gives `eth0` (D-192: Debian boots as `eth0`).
- `networkctl`: `eth0 routable configured`. IPv6 RA is accepted (networkd default), so the guest also gets a SLAAC address when the LAN sends RAs.

## One unchanged `netplan apply` (static guest, real agent)

- netplan 1.1.2 apply on networkd: `daemon-reload` (generator runs again), `udevadm` reload, then `networkctl reload` + `networkctl reconfigure` of every networkd link. It does not restart `systemd-networkd`.
- Result: rc 0 in 0.48 s. networkd logged `eth0: Reconfiguring with …10-netplan-eth0.network` (twice).
- `ip monitor`: the static IPv4 address was **not deleted** (re-announced only). The SLAAC IPv6 address and the RA default route were **deleted and came back after ~1.5–3 s**.
- Agent link **did not drop**: the same TCP connection to the controller (`:8444`, same local port) before and 30 s after, no agent journal line, `NRestarts=0`, Target `agent=online` with a new `last_seen`.
- Limit: one unchanged apply, IPv4 link only. A changed address or a renamed NIC was not tested, and an IPv6 link to the controller would see a short gap.

## Consequences for T-145 (`network` contract)

- Write netplan, not networkd files. Keep the cloud-init file name or drop a higher-numbered file; netplan merges `/etc/netplan/*.yaml` in lexical order.
- Match by MAC + `set-name: eth0`, as cloud-init does, so the generated `.link` stays the same.
- Consider `/etc/cloud/cloud.cfg.d/99-disable-network-config.cfg` (`network: {config: disabled}`), so cloud-init does not rewrite the file on a new instance-id.

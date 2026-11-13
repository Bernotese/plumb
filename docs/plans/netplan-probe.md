# Netplan cloud VM probe (T-149)

The `firewall-lxd` CI job runs `TestNetplanProbe` after the unchanged firewall
suite in each Debian 13 and Ubuntu 26.04 VM. Containers do not run the probe.
The test does not install netplan or change the cloud image network files.

The controller listens on the CI runner's LXD bridge. A real Plumb agent runs
inside the VM. Empty observe commands cross the VM NIC throughout an unchanged
`netplan apply`. The output records new server TCP connections, reconnect hello
reports and the maximum observe round trip. This measures application delivery
and TCP replacement, not packet loss. A negative result for an unchanged apply
does not prove that every future network change will preserve the connection.

Measured on 2026-11-13 in [CI run 43520120041](https://github.com/Bernotese/plumb/actions/runs/43520120041),
[cloud VM job](https://github.com/Bernotese/plumb/actions/runs/43520120041/job/132212115779),
commit `92cec7b`. Both existing firewall VM tests also passed.

| Fact | Debian 13 (LXD image only) | Ubuntu 26.04 |
|---|---|---|
| Source | `images:debian/13/cloud` (LXD cloud variant) | `ubuntu:26.04` (Ubuntu cloud image through LXD) |
| Guest version | Debian 13.7 (trixie) | Ubuntu 26.04.1 LTS |
| Netplan present | No in `images:debian/13/cloud`; see T-160 below for the vendor image | Yes, `/usr/sbin/netplan` |
| Active network service | `systemd-networkd`; NetworkManager inactive | `systemd-networkd`; NetworkManager inactive |
| `/etc/netplan/50-cloud-init.yaml` | Absent | Present; `ethernets.enp5s0.dhcp4: true` |
| Interface match | No netplan file to match | Interface name `enp5s0`; no `match`, MAC, `set-name` or explicit `renderer` in the file or merged configuration |
| Runtime renderer evidence | `networkctl`: routable, DHCP address on `enp5s0` | `networkctl`: `/run/systemd/network/10-netplan-enp5s0.network`, routable |
| `netplan apply` and agent TCP | Unavailable: netplan is not installed; no apply was attempted | No new TCP connections, no reconnect hello reports during apply and the probe window |
| Maximum observe round trip | Not measured | 8.611 ms |
| Probe elapsed | 0.42 s | 4.11 s (including 3.002 s of apply/concurrent observation) |

Image fingerprints from `volatile.base_image`:

- Debian: `d3116ba8b3d8c0df16ffc417f9bc59614070b5db84acdaf6075b2b05936cb658`
- Ubuntu: `881c273c21f4b25aee6b336e8af993166766b6c263032f9db7a61bdb037bc6a6`

The Debian source is the LXD distribution cloud variant, not the Debian vendor
QCOW2 used by a Proxmox Image. These measurements establish facts for the CI
images only. The probe itself added 4.53 s across both VMs, plus one test-binary
compilation and file copy; no additional VM or package installation was added.
The Debian VM source changed from the standard variant to the cloud variant.

The absence of netplan and `50-cloud-init.yaml` applies only to the LXD image
`images:debian/13/cloud`. The Debian vendor cloud image used by Plumb has
netplan 1.1.2 and cloud-init writes `50-cloud-init.yaml` with a MAC match and
`set-name`; see [T-160's vendor image facts](debian-network-facts.md).

C1b ([T-145](../tasks/T-145-slice5-contract-network-group.md),
[PR #244](https://github.com/Bernotese/plumb/pull/244), D-348) resolved both:
the agent writes under the netplan key of the entry with the main address
(without adding its own MAC match), and checks the merged result in a temporary
root before any change.

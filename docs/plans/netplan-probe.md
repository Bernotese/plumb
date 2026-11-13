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

Image results and the CI run link will be recorded after the first run.

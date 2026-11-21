# T-280 – Remove the test bridge vmbr9 from pve-dev (release slice C8)

- **Owner:** pl-backend (the only agent on the host for this card)   · **Reviewer:** pl-reviewer (the log)   · **QA:** –
- **Size:** S   · **Depends on:** the user's word of 2026-11-21 (D-444): `vmbr9` may be removed

## Goal
The isolated bridge `vmbr9` (D-379, made by T-224 for the NIC tests) leaves the test host. After it `pve-dev` has the network it had before T-224, and Plumb's install there has no extra bridge or VLAN right. This is the planned undo of T-224 step 8; `pl-lead` writes it as the rule of D-379 asks.

## Rules (hard)
- `AGENTS.md` "Test host" in full. Only what this card lists; anything else: ask `pl-lead` first. **If SSH to the host fails at any point: stop and tell `pl-lead` at once** (the user has console access).
- The only network change: `vmbr9` goes. **Never `ifreload -a`, never `systemctl restart networking`, never `ifdown`/`ifup` of another interface, never an edit of another stanza.** `vmbr0`, the routes, the addresses and the SSH setup stay byte for byte.
- No guest uses `vmbr9` (the pool is empty): check it first (`grep vmbr9` in every guest config on the node: 0 hits) and stop if one does.
- Installer runs: `--dry-run` → the result to `pl-lead` → the go → the real run. No address, MAC, key or local time in the log.

## Steps
- [ ] 0 Start state, read-only: the controller version (RC6), the pool empty, the ACL view (22 rows), `storage.cfg` and `/etc/network/interfaces` copied to 0600 files on the host (with their sha256 in a 0600 file, not in the log); `ifquery -a -c` clean (stop if not); `ip -br link`, `ip -br addr`, `ip route`, `bridge vlan show` recorded (0600). Compare `/etc/network/interfaces` minus the `vmbr9` stanza with the copy `interfaces.before-T224` if that file is still on the host: equal, or say exactly what differs and stop.
- [ ] 1 The repair run of the installer WITHOUT `--extra-bridge vmbr9 --extra-vlan vmbr9:20` (the other flags as today; the printed rerun line of T-269 shows them): dry run → `pl-lead` → go. Expected: exactly 4 `pveum acl delete` lines (`vmbr9` and `vmbr9/20`, user + token), nothing else; after the real run the ACL view has 18 rows (9 lines), `PLUMB_PVE_EXTRA_NETS` is empty, the controller is active, `install.state` has no extra net.
- [ ] 2 The bridge, exactly as T-224 step 8: `ifdown vmbr9`; remove exactly the stanza that T-224 appended (show the diff of the file before you write it: only those lines); `ip link show vmbr9` → gone; `ifquery -a -c` clean; link/address/route/bridge-vlan records before = after except the `vmbr9` lines; `pvesh get /nodes/<node>/network` no longer lists `vmbr9`.
- [ ] 3 Plumb after it: Settings → General (API read) shows 9 rights lines, all "there"; a Target proposal with a NIC on `vmbr9` → 422 `network-not-allowed`; one VM on `vmbr0` is made, enrolls and is deleted again (the network of new guests still works).
- [ ] 4 End proof: only LXC 100 on RC6, all stores empty, ACL 18 rows, `storage.cfg` byte-equal, `/etc/network/interfaces` = the state before T-224, no test token, no open Issue; the start copies and this run's folder removed; test secrets deleted (D-416). The log `docs/tasks/T-280.log.md` as a PR (leak grep incl. IPv6: counts only).

## Result (owner, max 8 lines)
branch · commit · tests · notes

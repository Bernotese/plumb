# T-224 – `pve-dev`: make the isolated bridge `vmbr9` (D-379, D-414)

- **Owner:** pl-backend (the one agent on the host)   · **Reviewer:** pl-reviewer (reads the log)   · **QA:** –
- **Size:** S   · **Depends on:** **the user confirms a console path to `pve-dev`** (asked by pl-lead; do not start before pl-lead says so)

## Goal
Slice 7 needs a second bridge for the real proof of more NICs and VLAN tags. The user allowed exactly this one change of the network config of `pve-dev` (D-379). It must not touch `vmbr0`, the SSH path of the host.

## Allowed host actions (exactly these, in this order; anything else: stop and ask pl-lead)
1. Copy `/etc/network/interfaces` to a 0600 file outside the repo (on the host: `/root/plumb-install/interfaces.before-T224`; and note its sha256).
2. `ifquery -a -c` (running state against the file). **Stop** if anything differs; report.
3. Record `ip -br link`, `ip -br addr`, `ip route`, `bridge vlan show` (before) to a 0600 file.
4. Append this stanza to `/etc/network/interfaces` (PVE ignores `interfaces.d`), nothing else:
   ```
   auto vmbr9
   iface vmbr9 inet manual
       bridge-ports none
       bridge-stp off
       bridge-fd 0
       bridge-vlan-aware yes
       bridge-vids 2-4094
   ```
5. `ifquery vmbr9` (syntax), then bring up **only** `vmbr9`: `ifup vmbr9`. **Never `ifreload -a`, never `systemctl restart networking`, never `ifdown`/`ifup` of another interface.**
6. Check `ip -d link show vmbr9` (`vlan_filtering 1`, no port, no address).
7. Record link, addresses, routes, bridge vlans again (after). Only lines of `vmbr9` may differ. If anything of `vmbr0` or any other line differs: **undo at once** (step 8) and report.
8. Undo (only on a failed check now; the planned undo is a later card of pl-lead): `ifdown vmbr9`, remove exactly the appended stanza, check `vmbr9` is gone, compare before = after, compare the file's sha256 with the copy.
- No address on `vmbr9`, no port, no firewall change, no SDN object, no ACL change (the ACL lines come with the installer in T-218).
- Keep one SSH session open during steps 4–7 and test a **second, new** SSH login after step 5 before closing the first.

## Acceptance criteria
- [ ] The steps above, with the before/after files (0600, on the host) and their diff in the log (only `vmbr9` lines). `pvesh get /nodes/<node>/network` lists `vmbr9` as a VLAN-aware bridge without ports.
- [ ] `docs/tasks/T-224.log.md` (no address, no MAC); PR to `main`; report to `pl-lead`.

## Out of scope
- Everything else on the host.

## Result (owner, max 8 lines)
branch · commit · notes

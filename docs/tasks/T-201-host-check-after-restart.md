# T-201 – Read-only check of the test host after the herdr restart

- **Owner:** pl-backend (the one agent on the host)   · **Reviewer:** –   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
The herdr server was down from 05:36 to 09:23 UTC on 2026-11-14. No host card was active, but the user asks for a check that nothing is left half-done on `pve-dev`.

## Allowed host actions
Read-only only: `pvesh get` / `qm list` / `pct list`, pool `plumb` members, content of `plumb-images`, `pveum acl list` for the Plumb user/token (path + role only), sha of `storage.cfg` against the T-158 end copy, `systemctl is-active plumb` in LXC 100 and the controller version, running tasks (`pvesh get /nodes/<node>/tasks --running 1`). Nothing else. No cleanup: report what you find.

## Acceptance criteria
- [ ] Report to `pl-lead` (max 8 lines, no address / id): guests on the node, pool and `plumb-images` content, ACL lines count and same/different, `storage.cfg` same/different, controller version and state, running tasks, any `ssh`/installer process of ours still alive on the dev VM (`pgrep -a ssh` filtered to the test host, no values printed).

## Result (owner, max 8 lines)
notes

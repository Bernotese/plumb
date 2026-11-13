# T-139 – Prep: facts from the test host for the coming topics

- **Owner:** pl-backend (the one agent on the host)   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** –

## Goal
The next slices touch Proxmox in new places. Collect the facts now so that the plans rest on the real host (PVE 9.2), not on guesses. Also bring the test controller to the latest `main`.

## Context (read only these)
- `AGENTS.md` "Test host (Proxmox)" and "Autonomy and release" · memory notes on the host
- `docs/tasks/T-122.log.md` (state of the host) · `docs/DECISIONS.md` D-081, D-239, D-240, D-324

## Allowed host actions
Part A, read-only (`pvesh get`, `pveum … list`, `cat` of config files, API GETs with the Plumb token):
- Network: bridges and VLAN awareness, what a second NIC on a guest needs; which privilege and ACL path the token would need for another bridge or an SDN vnet. **Find out only. Do not add or change any ACL, role or bridge.**
- Adoption: what the Plumb token can see of a guest outside the pool `plumb` today, and which right would be missing to manage one.
- Guest backups: what `vzdump` of a guest in the pool needs (privileges, storage content `backup`), what the token has today.
- SSH repair fallback: how the controller in LXC 100 can reach a guest (routes, which key could be used, what cloud-init / `pct` set up today).
- Signed image checksums: which signature files Debian and Ubuntu publish next to the cloud images and LXC templates Plumb downloads (this part needs no host).
Part B, after Part A: bring the controller to the latest `main` with the installer (repair/update run). `--dry-run` first, send the result to `pl-lead`, wait for the go.

Nothing else. No new guest, no new storage, no change to network, SSH, `local`, `local-lvm`.

## Acceptance criteria
- [ ] `docs/plans/host-facts.md` on your branch: one short section per topic, facts with the command class that showed them, and a line "needs a new token right: yes (which) / no". No address, MAC, token id, key, password.
- [ ] Push the branch, no PR. Report to `pl-lead`.
- [ ] Part B: dry-run result to `pl-lead`; after the go, real run; version and health of the controller in the report.

## Out of scope
- Any change to rights. Code.

## Result (owner, max 8 lines)
branch · commit · tests · notes

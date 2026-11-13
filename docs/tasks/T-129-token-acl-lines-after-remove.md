# T-129 – Token removal leaves its ACL lines (T-122 F4)

- **Owner:** pl-reviewer (as implementer)   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S   · **Depends on:** T-114 (on main)

## Goal
After the installer removes the Plumb token, no ACL line of that token stays in the Proxmox config.

## Context (read only these)
- `docs/tasks/T-122.log.md` F4 (when it is on main; until then ask pl-backend): on PVE 9.2.26 `pveum user token remove` leaves the 7 ACL lines of the token; every `pveum`/`qm`/`pct` then prints "ignore invalid acl token" 7 times; a new token with the same name gets these lines again
- `install/install.sh`: token removal in `--remove` and `--remove --keep-guests`; the comment that says the ACLs go with the token; D-081 (seven roles, one per path), D-240 (`PlumbAudit` on `/`)

## Acceptance criteria
- [ ] `--remove` and `--remove --keep-guests` delete the ACL entries of the token explicitly (`pveum acl delete … --tokens …`, each path and role that the installer set) before or with the token; the ACLs of the user stay as the contract says (D-290: `--keep-guests` keeps the user with its ACLs)
- [ ] `--restore` and a repair run set the token ACLs themselves and do not rely on leftover lines; a leftover line from an older removal is cleaned
- [ ] No right is added: the set of token ACLs after install, repair and restore equals today's set (test compares it)
- [ ] Fake-PVE test that models the PVE 9.2 behaviour (token removal keeps the lines); dry-run shows the new steps; shellcheck clean; `make lint test build` green
- [ ] The wrong comment in `install.sh` is fixed

## Out of scope
- A host run (pl-lead decides; T-122 can check it at the end)
- No new token right (D-081)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-079 – Proxmox driver for LXC

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** T-076, T-077; step 1 needs nothing
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
The driver creates, observes, changes and deletes unprivileged containers from a repacked template.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §3, §6, §9 (D-227, D-229, D-231, D-233, D-237)
- `docs/tasks/T-038.log.md`; AGENTS.md "Test host (Proxmox)"; memory notes on the host

## Acceptance criteria
- [ ] **Step 1, first, stop and report (condition of the user, D-238): host check H10.** With the Plumb token of today, in the pool `plumb`, VMIDs 9000–9099: can it create a container with `unprivileged=0`? set `features` `keyctl`, `fuse`, `mknod`, `mount=…`? set `mpN` or `devN` with a host path (at create and by a config change)? Record each call and the answer of Proxmox (status + message) in `docs/tasks/T-079.log.md` (no address, no token id). Remove every guest you made. Do not change rights, storages or the controller. Report to pl-lead and wait: the user decides about the threat model before driver code.
- [ ] Step 2 (after the go): everything in §3 of the plan, host recordings for `pvefake`, the mode of `/var/lib/lxc/<id>/config`, H11.
- [ ] Tests: `make lint test build` passes; leak scan of the recordings incl. `env`.
- [ ] New decisions go to `docs/decisions/T-079.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- Engine, web, image builds.

## Result (owner, max 8 lines)
branch · commit · tests · notes

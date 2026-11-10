# T-015 – Slice acceptance tests

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** T-011; real-host part needs T-012, T-013   · **Parallel with:** T-014
- **Status:** proposed (do not start before the user approves the architecture)

## Goal
The team has proof that the first slice works through the public API, with and without a real Proxmox host.

## Context (read only these)
- `docs/ARCHITECTURE.md` §8
- `docs/BRIEF.md` §8.2
- Files to add: `test/slice/slice_test.go`, `test/pve/slice_pve_test.go`, `docs/tasks/T-015.log.md`

## Acceptance criteria
- [ ] This test uses only HTTP (`/api/v1`) with API tokens. The in-process test of T-010 stays the test of the core.
- [ ] Without Proxmox (`make test`): create a target with `deploy-user`, `motd`, `nginx` → plan → approve → run succeeds → change the Nginx site file in the guest root → an Issue opens within 10 s → `revert` closes it and the Plan shows the Nginx restart → `accept` on a second drift creates a new revision.
- [ ] Real host (`make test-pve`): the same path with a real Ubuntu 26.04 VM and a real Debian 13 VM, plus a reboot. Change the VM memory in Proxmox → an Issue opens on `guest:config`.
- [ ] The log file records commands, results and durations. No secrets in the log.
- [ ] Each test file ≤ ~150 lines. Bugs go to `pl-lead` as short reports, not as fixes.

## Out of scope
- Fixing product code.
- Token tier checks (T-011 has them).

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

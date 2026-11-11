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
- [ ] Real host (`make test-pve`; blocked until `pl-lead` confirms the test host, D-041 — report the part without Proxmox first): the same path with a real Ubuntu 26.04 VM and a real Debian 13 VM, plus a reboot. Change the VM memory in Proxmox → an Issue opens on `guest:config`.
- [ ] The log file records commands, results and durations. No secrets in the log.
- [ ] Each test file ≤ ~150 lines. Bugs go to `pl-lead` as short reports, not as fixes.

## Real-host part (pl-lead, 2026-11-11) – go
The test host is ready and the controller runs there (T-053, log `docs/tasks/T-053.log.md`). Rules: AGENTS.md "Test host". `pl-codex-qa` is the one agent on the host for this part.
- Settings: `~/.config/plump/testhost.env` (never in the repo): controller URL, Plumb CA file, API token file, admin password file, two static VM addresses + gateway (`PLUMB_TEST_VM_IP_1/2`, `PLUMB_TEST_VM_GW`), guest SSH test key. `pl-backend` fills in the controller entries before you start. The test skips without them.
- Test: `test/pve/slice_pve_test.go` (build tag `pve`, each file ≤ ~150 lines), second step of `make test-pve`. The driver tests and this test share the pool and the VMID range 9000–9099: never run both at once.
- Path: the criterion above, with a Debian 13 VM and an Ubuntu 26.04 VM (image documents with pinned SHA-256; the host downloads them), static addresses (D-079), a reboot, and the `guest:config` drift (limit ~90 s, `PollGuests` runs every 60 s). Agent drift opens an Issue within 10 s.
- Root actions on the host, only these: `qm set <vmid> --memory …`, `qm reboot <vmid>`, and at the end `qm destroy` of the image templates (the driver never deletes a template, D-077). Only VMIDs in 9000–9099 that the test made.
- Also allowed, read-only (pl-lead, 2026-11-11): `qm list` and `qm config <vmid>` to prove ownership before a destroy. Destroy only a VM that has a VMID in 9000–9099, is a template, is in the Plumb pool and has exactly the name `plumb-img-<name>-<sha256[:12]>` (D-077) of an Image document that this test run created. If one check fails: no destroy, the test fails and names the VMID.
- Split (the Codex sandbox has no SSH to the host): `pl-codex-qa` writes the test, `pl-backend` runs it on the host.
- A destructive Plan (delete of a Target) needs a human approval: use an admin session with the password file for that step, and say so in the log.
- End state: no guest, no volume, no template of the test left; the controller LXC stays. No address, token, password or key in the log, the PR or the report.
- A product bug goes to `pl-lead` as a short report, not as a fix.

## Out of scope
- Fixing product code.
- Token tier checks (T-011 has them).

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

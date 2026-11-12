# T-084 – the token gets `Sys.Audit` (D-239)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** – (installer part after T-077 is merged)
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
Plumb reads the thin-pool metadata use, so the disk gate and the Plan no longer say "unknown". User decision D-239.

## Context (read only these)
- `install/install.sh` (roles, D-081), `install/install_test.go` (the list of rights the token must not have), `internal/driver/proxmox/images.go` `DiskSpace` (it already reads `/nodes/<node>/disks/lvmthin`), the installer and rights docs
- AGENTS.md "Test host (Proxmox)"

## Acceptance criteria
- [ ] **Step 1, stop and report (the user wants to know the path):** find the narrowest ACL path that makes the read of `DiskSpace` work. Read the permission check of the API call in the PVE source on the host (read-only), then prove it on the host with the real token. Try `/nodes/<node>` first (role `PlumbNode`); name what else `Sys.Audit` on that path lets the token read. If only `/` works, stop: pl-lead asks the user. Write the result to `docs/tasks/T-084.log.md` and `docs/decisions/T-084.md` (D-240). Any ACL you set by hand for the proof is removed again; the installer sets the final one.
- [ ] Step 2: the installer sets it (new install and repair run, shown in the plan, D-084); `--dry-run` and the "it gets no …" text are right; the test list of forbidden rights is updated; docs name the right, the path and the reason.
- [ ] Step 3: repair run on `pve-dev` (dry-run first, result to pl-lead, then the real run), with current `main` on the host. Proof: the Plan/disk gate shows the metadata use, not "unknown".
- [ ] Tests: `make lint test build` passes.
- [ ] New decisions go to `docs/decisions/T-084.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- Any other new right.

## Result (owner, max 8 lines)
branch · commit · tests · notes

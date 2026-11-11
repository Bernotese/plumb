# T-073 – A failed guest create must not leave a VM behind

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** –   · **State:** go (product defect found on the real host, T-039 host facts A4, 2026-11-11)

## Goal
When a guest create fails after the clone, Plumb removes the cloned VM or says clearly that it is left, and a later Plan can go on.

## Situation
- Real host (log `docs/tasks/T-039.log.md` on `origin/pl-backend/T-039-host-facts`, id A4): a Target with `disk` 8 GB from a template with a 10 GB disk. The Run fails with "shrinking disks is not supported", and Plumb LEAVES the cloned VM. There is no binding, so Plumb does not know the VM; it uses a VMID, disk space and the name.
- T-012 review S1 made `removeHalfMade` return its errors for the image template path. The guest create path after the clone (config, resize, start) needs the same rule.

## Context (read only these)
- `internal/driver/proxmox/driver.go` (Create, removeHalfMade, the clone + config + resize steps), its tests and `pvefake_test.go`
- `internal/plan/run.go` (create step), `docs/DECISIONS.md` D-077, D-188

## Acceptance criteria
- [ ] A disk smaller than the template disk is refused before the clone (the driver knows the template's disk size), with a text that names both sizes. Check if this can also be a save-time or plan-time error; if it is cheap, do it there too.
- [ ] Any failure after the clone (config, resize, start): the driver removes the VM it just made (only that VMID, proven as ours as in `removeHalfMade`), and the error names the cause. If the removal fails, the error names the VMID and the `qm destroy` hint, and the next Plan for the Target handles the leftover (say how).
- [ ] No path deletes a VM that this create did not make. Tests with the fake PVE for: resize failure, config failure, start failure, removal failure.
- [ ] `make lint test build` pass. New decision only if a rule changes (ask pl-lead for a number).
- [ ] Real-host check by pl-backend after the merge: the A4 case leaves no VM.

## Out of scope
- Image builds (T-039).

## Result (owner, max 8 lines)
branch · commit · tests · notes

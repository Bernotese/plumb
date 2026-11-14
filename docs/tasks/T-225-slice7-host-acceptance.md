# T-225 – Slice 7 acceptance on the real host

- **Owner:** pl-backend (the one agent on the host)   · **Reviewer:** pl-reviewer (reads the log)   · **QA:** T-226
- **Size:** M   · **Depends on:** T-218 … T-223 merged; T-224 (vmbr9) done

## Goal
Slice 7, child card **C9** of T-215. Prove slice 7 on `pve-dev`.

## Context (read only these)
- Plan: `docs/tasks/T-215-slice-7-plan.md` section "Plan" (rows, shared-file order, rules of D-413), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §20 (PR #325 until merged), decisions D-398 … D-415 and the amendments of T-217; host facts on branches `pl-backend/T-214-host-facts-slice-7`, `pl-backend/T-139-prep-host-facts`; risks §6 on `pl-reviewer/T-140-risk-draft`. Read row C9, the contract parts it names and the review comments of #321 / #325 on it.
- `AGENTS.md` "Autonomy and release" (D-326), "Flow" step 5 (D-394), "Test host".

## Acceptance criteria
- [ ] Everything row C9 lists; touch only the files that row gives you (test files next to them need no question; single wiring lines in `plan.go`, `run.go`, `serve.go`, `handlers.go`, `views.go`, `api.go` neither: list them in the PR). Another file: ask `pl-lead` first.
- [ ] The "Real-host acceptance" steps of the plan, nothing else; step C9.10 uses `plumb-snippets`, never `local-lvm`; `storage.cfg` copied first and byte-equal after. Installer `--dry-run` first, result to `pl-lead`, wait for the go. Backup first; row counts before = after for the new migrations.
- [ ] A token step for every human-only rule of the slice (`guest-restore`, backup delete, lowering keep). Root cleanup only for objects of this run, after the go of `pl-lead`; `vmbr9` stays until its undo card. Test secret files are deleted at the end by D-416 (one line in the report).
- [ ] Log `docs/tasks/T-225.log.md` without addresses, MACs, token ids, keys; a claim says what was really exercised.
- [ ] Rules of D-413: a new command type goes through the real link in a test; every per-Target table says what happens at delete, unbind and VMID reuse (D-397); a new operation updates `roleHints.ts`, the tier test and the contract marks; a new resource attribute gets the guest-value classification test (D-359). Name in the report the CI job that proves the card and which of your tests skip locally.
- [ ] Rights exactly D-379 / D-380, never more. New decisions only with numbers from `pl-lead`. Migrations: the next free number at merge time (D-337).
- [ ] `make lint test`; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. A restore after the guest is gone (D-411). Any release step. The user's own installation (U7).

## Result (owner, max 8 lines)
branch · commit · tests · notes

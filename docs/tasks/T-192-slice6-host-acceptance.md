# T-192 – Slice 6 acceptance on the real host (part A and part B)

- **Owner:** pl-backend (the one agent on the host)   · **Reviewer:** pl-reviewer   · **QA:** T-195
- **Size:** L   · **Depends on:** all cards of part A and B merged

## Goal
Slice 6 part B, child card **A13 + B7** of T-172. Prove slice 6 on `pve-dev` in one run (T-187 is folded into this card).

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` part B (as updated by T-189), contract `api/openapi.yaml` + `docs/ARCHITECTURE.md` §19, decisions D-372, D-373, D-377, D-378, D-382 … D-389 (until PR #290 is merged: branch `pl-architect/T-189-contract-b`), risks `docs/plans/risks.md` §2, §3 on branch `pl-reviewer/T-140-risk-draft`. Read row A13 + B7, the test table of ARCH §19 (each rule names its test) and the review comments of PR #290.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A13 + B7 lists; touch only the files that row gives you (test files next to them need no question; list them in the PR). Another file: ask `pl-lead` first.
- [ ] Every test that ARCH §19 names for this row exists under that name.
- [ ] The "Real-host acceptance" steps of part A and part B of the plan, nothing else. A token step for every human-only rule of this slice (`firewall-off`, Accept to a parameter, repair, adopt, delete_guest). Live drift also in an LXC. Repair of a VM and an LXC with a stopped agent; a changed host key refuses. Adoption of a hand-made guest that the test puts into the pool `plumb` itself (allowed: it is a test guest made for this step; no guest outside the pool is touched).
- [ ] Installer `--dry-run` first, result to `pl-lead`, wait for the go. Backup first; row counts before = after for migrations 0020 … 0023. No change of rights, network, SSH config of the host, `local`, `local-lvm`. Root cleanup only for objects of this run, after the go of `pl-lead`. Secrets of the run in a 0600 file outside the repo; say at the end that it can be deleted.
- [ ] Log `docs/tasks/T-192.log.md` without addresses, MACs, token ids, keys, passwords; a claim says what was really exercised.
- [ ] A card that adds an API operation updates `roleHints.ts`, the tier test and the contract marks in the same PR. No new token right. New decisions only with numbers from `pl-lead`.
- [ ] `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. The guest-agent transport (not built, D-377). Slice 7.

## Result (owner, max 8 lines)
branch · commit · tests · notes

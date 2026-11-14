# T-187 – Slice 6 part A acceptance on the real host

- **Owner:** pl-backend (the one agent on the host)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-186)
- **Size:** M   · **Depends on:** T-174 … T-185, T-196 merged

## Goal
Slice 6 part A, child card **A13** of T-172. Prove part A on `pve-dev`.

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions D-360 … D-376 (`docs/decisions/T-172.md` on that branch), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §18 (on main). Read row A13 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A13 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] The steps of "Real-host acceptance" part A in the plan, nothing else. Lessons of slice 5: a host step with a token for **every** human-only rule of this slice (`firewall-off`, Accept to a parameter); live drift also in an LXC; backup first, row counts before = after for migration 0020.
- [ ] Installer `--dry-run` first, result to `pl-lead`, wait for the go. No change of rights, network, SSH, `local`, `local-lvm`. Test objects through Plumb only, removed at the end; root cleanup only for objects of this run and only after the go of `pl-lead`. Secrets of test users in a 0600 file outside the repo.
- [ ] Log `docs/tasks/T-187.log.md` without addresses, MACs, token ids, keys, passwords. A claim says what was really exercised (review of #265).
- [ ] A card that adds an API operation updates `web/src/lib/roleHints.ts`, the tier test and the contract marks in the same PR. A new resource type or attribute gets the opaque/guest-value classification test (D-359).
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-187.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

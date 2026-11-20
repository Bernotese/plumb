# T-174 – Settings server (backup schedule, Proxmox view)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-186)
- **Size:** M   · **Depends on:** T-173 merged

## Goal
Slice 6 part A, child card **A2** of T-172. Backup time and keep become editable in Plumb; a read-only Proxmox view with an ACL check (D-361).

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions D-360 … D-376 (`docs/decisions/T-172.md` on that branch), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §18 (on main). Read row A2 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A2 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Migration 0020. The `proxmox` part of `GET /settings` goes only to a human session; a token gets `proxmox: null` (test). `PATCH /settings` by a token → 403.
- [ ] The ACL check uses only read calls the token may already do; it never changes a right. No secret, token id or full token name in the view.
- [ ] A card that adds an API operation updates `web/src/lib/roleHints.ts`, the tier test and the contract marks in the same PR. A new resource type or attribute gets the opaque/guest-value classification test (D-359).
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-174.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

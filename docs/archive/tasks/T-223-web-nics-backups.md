# T-223 – Web: NIC list, backups tab, restore wizard

- **Owner:** pl-frontend   · **Reviewer:** pl-architect   · **QA:** T-226
- **Size:** M   · **Depends on:** T-217 (mock), T-221, T-222

## Goal
Slice 7, child card **C7** of T-215. The UI for more NICs and guest backups.

## Context (read only these)
- Plan: `docs/tasks/T-215-slice-7-plan.md` section "Plan" (rows, shared-file order, rules of D-413), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §20 (PR #325 until merged), decisions D-398 … D-415 and the amendments of T-217; host facts on branches `pl-backend/T-214-host-facts-slice-7`, `pl-backend/T-139-prep-host-facts`; risks §6 on `pl-reviewer/T-140-risk-draft`. Read row C7, the contract parts it names and the review comments of #321 / #325 on it.
- `AGENTS.md` "Autonomy and release" (D-326), "Flow" step 5 (D-394), "Test host".

## Acceptance criteria
- [ ] Everything row C7 lists; touch only the files that row gives you (test files next to them need no question; single wiring lines in `plan.go`, `run.go`, `serve.go`, `handlers.go`, `views.go`, `api.go` neither: list them in the PR). Another file: ask `pl-lead` first.
- [ ] The restore wizard states the human-only rule and what is lost; delete of a backup with confirm (admin session only); the encryption state and the clear-text warning are shown; the token's own power at the PVE API is said in one plain sentence (M3 of #321).
- [ ] Rules of D-413: a new command type goes through the real link in a test; every per-Target table says what happens at delete, unbind and VMID reuse (D-397); a new operation updates `roleHints.ts`, the tier test and the contract marks; a new resource attribute gets the guest-value classification test (D-359). Name in the report the CI job that proves the card and which of your tests skip locally.
- [ ] Rights exactly D-379 / D-380, never more. New decisions only with numbers from `pl-lead`. Migrations: the next free number at merge time (D-337).
- [ ] `make lint test`; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. A restore after the guest is gone (D-411). Any release step. The user's own installation (U7).

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-148 – Copy rule for guest-reported values (D-331)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-157)
- **Size:** M   · **Depends on:** T-144 merged

## Goal
Slice 5, child card **C4** of T-138. A token can no longer bring a value that a guest reported into the desired state as a normal change; closes the D-323 limit for every client, also MCP later.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan", decisions D-327 … D-347 (`docs/DECISIONS.md`; until merged in `docs/decisions/T-138.md`, `T-144.md`), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §17. Read row C4 of the child card table, the contracts, the risks, and the review comments of PR #241 / #243 that name this row.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C4 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Owns `internal/plan/humanonly*.go` and `internal/plan/run.go` (Engine.Approve), migration 0018. Match at plan time and again at approve time: any Target, open and closed Issues, per list element, by hash.
- [ ] Tests for each bypass of review H1 (ignore once then copy, guest closes its Issue, other Target, one list element) and for F1: the cap never evicts; overflow flags the Target (every Plan human-only for 30 days) and opens the Issue.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-148.md`.
- [ ] No new token right. Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

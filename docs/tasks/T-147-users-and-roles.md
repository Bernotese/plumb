# T-147 – Users and roles

- **Owner:** pl-reviewer (implementer)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa (T-157)
- **Size:** L   · **Depends on:** T-144 merged; `api.go` after T-146

## Goal
Slice 5, child card **C3** of T-138. More than one user, with roles `read` < `plan` < `apply` < `admin`; closes F3 of T-122.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan", decisions D-327 … D-347 (`docs/DECISIONS.md`; until merged in `docs/decisions/T-138.md`, `T-144.md`), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §17. Read row C3 of the child card table, the contracts, the risks, and the review comments of PR #241 / #243 that name this row.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C3 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Migration 0017. Every case of `docs/plans/risks.md` §1 (branch `pl-reviewer/T-140-risk-draft`) has a test: self-raise, token path, last-admin race (one transaction), old sessions and tokens after a role change or disable, name reuse, login limit that always ends.
- [ ] Review M2: the `p.human()` checks in `api.go` / `handlers.go` also check the role; `handlers.go` is in your file list.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-147.md`.
- [ ] No new token right. Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

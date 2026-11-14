# T-170 – API: a pending Plan lists the document changes it will commit

- **Owner:** pl-architect (implementer)   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S–M   · **Depends on:** –

## Goal
Server part of host finding F1 (T-168 is blocked on it): the API gives no document changes for a pending Plan, so the Plan screen cannot show what a Block-only Plan changes and a person approves blind.

## Context (read only these)
- `docs/tasks/T-168-plan-view-document-changes.md` and the blocked report of pl-frontend (proposal below)
- `api/openapi.yaml` (`Plan`, `RevisionDiff.changes`) · `internal/plan/run.go` (the apply that Approve commits) · `internal/api/views.go`

## Acceptance criteria
- [ ] `Plan.documents: [{kind, name, op: added|changed|removed, before, after}]`, same item shape as `RevisionDiff.changes`, computed as base revision vs `apply(base, proposal)` with the **same** code that Approve commits, so the screen can never show something else than what is applied. Contract + ARCH line.
- [ ] Stale Plans, rollback Plans, pin_refresh Plans: say what the field holds for each. No secret value in it (documents hold references only: prove with a test that plants a secret and greps the answer).
- [ ] Size bound for big documents (say the rule).
- [ ] Tests incl. the F1 case (catalogue update of a Block that no Target uses). `make lint test` passes. PR to `main`; tell `pl-lead` and `pl-frontend` when it is merged-ready (pl-frontend builds T-168 on the contract).

## Out of scope
- The web part (T-168).

## Result (owner, max 8 lines)
branch · commit · tests · notes

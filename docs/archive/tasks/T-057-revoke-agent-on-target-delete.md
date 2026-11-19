# T-057 – Revoke the agent when its Target is deleted

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** –   · **State:** go (security fix; found by pl-architect while planning T-039, rated MEDIUM by pl-reviewer on 2026-11-11)

## Goal
After a Target was deleted through a Plan, its agent credential is dead: the old agent cannot poll, say hello or send reports.

## Situation (pl-reviewer, main c559312)
- The delete keeps the agent row and `ts.agentID` (`internal/plan/run.go:409-417`). The old bearer credential (in the guest disk, a backup, or a template made from the guest) stays valid: it can poll, hello and send drift reports. It cannot enroll again (one-time token) and cannot block a new Target with the same name.
- Secret leak: when a Target with the same name comes back, a stale hello between the new revision and the approval gets `sendWatch` with the new Target's desired state, incl. secret values (`internal/plan/plan.go:268-275, 342-347`).
- Trusted reports: drift reports go to `issue.Intake` with no binding check, so the old agent can make Issues and events for a deleted or re-added Target, with no rate limit (the database grows).

## Context (read only these)
- `internal/plan/run.go` (delete case), `internal/plan/plan.go` (hello, sendWatch), `internal/link` (agent rows, enroll tokens, wake), `internal/issue` (Intake)
- `docs/DECISIONS.md` D-156, D-157

## Acceptance criteria
- [ ] The delete step revokes the agent before the binding is deleted: the agent row and the enroll tokens of the Target are removed, the in-memory agent id is marked revoked, an open poll ends.
- [ ] A request with the credential of a deleted Target gets 401 on every link route (poll, hello, report). Test.
- [ ] A Target that is created again with the same name: the OLD credential gets 401 at every point of the new Target's life (before the approval, during the create, after the enrollment) and never receives a watch list or a secret value. Test.
- [ ] Reports for a Target without a binding are dropped and make no Issue and no event. Test.
- [ ] A failed delete Run (the guest was not deleted) does not revoke the agent. Test.
- [ ] No migration, or say why one is needed (0008 is free).
- [ ] New decision D-211 in `docs/decisions/T-057.md`. `make lint test build` pass.

## Out of scope
- `link.Revoke` for image build guests (T-039 uses the function of this card).
- A rate limit for authenticated reports of a live agent (say in the Result if you think it needs a card).

## Result (owner, max 8 lines)
pl-frontend/T-057-revoke-agent · see PR · `make lint test build` pass; new tests `internal/link/revoke_test.go` (TestRevoke), `internal/issue/revoke_test.go` (delete, re-add, no binding, failed delete) · no migration. Rate limit for reports of a live agent: no card needed now; a live agent is a bound guest, and its drift is one open Issue per resource.

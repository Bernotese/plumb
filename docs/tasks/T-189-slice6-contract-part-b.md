# T-189 – Slice 6 contract part B (SSH repair, adoption)

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** –

## Goal
The user answered: U1 (a) → D-377 (repair SSH key, no new right), U2 (a) → D-378 (only guests a person puts into the pool). Part B of slice 6 starts. Child card **B1** of T-172.

## Context (read only these)
- `docs/tasks/T-172-slice-6-plan.md` part B (rows B1 … B7, contract text for U1 (a) and U2 (a), D-372, D-373) · `docs/DECISIONS.md` D-377, D-378 · `docs/plans/risks.md` §2, §3 (branch `pl-reviewer/T-140-risk-draft`)
- Card numbers: B1 T-189, B2 T-190, B3a T-191, B3b – not built, B4 T-193, B5 T-194, B6 T-195, B7 T-192

## Acceptance criteria
- [ ] Everything row B1 lists for the answers (a)/(a): routes, the repair bundle format, the SSH transport contract, the adoption contract; `api/openapi.yaml` with `x-plumb-planned` marks, `x-plumb-role`, `x-plumb-rate`, `web/src/lib/roleHints.ts` for the new operations, ARCH §18.
- [ ] Update the plan card in the same PR: drop B3b and the U1 (b)/(c), U2 (b) branches; fix the shared-file order for part B against what part A really touched (`api.go`, `propose.go`, `humanonly.go`, `views.go`, `go.mod`).
- [ ] Every rule of risks.md §2 and §3 is named in the contract with the test that proves it (key never in an export / log / unencrypted backup; host key pinned at first enrollment, a changed key refuses; repair and adoption Plans are human-only; delete of an adopted Target never deletes the Proxmox guest unless the Plan says so in clear words; `cipassword` and other secrets of an adopted guest are never stored or shown).
- [ ] No new token right (prove: list the PVE calls of part B and the ACL line that already allows each).
- [ ] Decision numbers D-382 … D-389 in `docs/decisions/T-189.md`. `make lint test`; PR to `main`; pl-reviewer checks; report.

## Out of scope
- Code. Slice 7.

## Result (owner, max 8 lines)
branch · commit · tests · notes

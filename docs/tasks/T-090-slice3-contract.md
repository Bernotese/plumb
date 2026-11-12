# T-090 – Contract for slice 3

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** –   · **Parent:** T-089 (C1)

## Goal
Write the contract that all slice 3 cards build on: command `update` (SHA-256 only), capability list in `hello`, agent version and capabilities in the Target status, the version gate (refusal text, check at Plan and at Run start), the "update agent" Plan step, and the schemas of `apt_repository`, `unit`, `sysctl`, `command`.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-090.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [ ] OpenAPI, `schema/*.json`, wire types and ARCHITECTURE text for all items above (D-257 … D-260); no behaviour yet
- [ ] `make lint test build` passes; `check:api` passes
- [ ] Push the wire and schema part first and report it, so T-091, T-093 and T-094 can start against it

## Out of scope
- Any agent or controller behaviour

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-099 – QA slice 3: tests against T-091 … T-097

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M (one narrow task per card)   · **Depends on:** each card   · **Parent:** T-089 (C9)

## Goal
Each slice 3 card gets a second opinion: tests against its acceptance criteria.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-099.md`; ask pl-lead for numbers (next free: D-263)

## Acceptance criteria
- [ ] Per card, when pl-lead sends it: extend tests on a QA branch based on the card's PR branch, small diff
- [ ] Mutation audit of the version gate and of the update hash check
- [ ] Report per card to pl-lead (max 8 lines)

## Out of scope
- Fixes in product code

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

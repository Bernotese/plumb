# T-098 – QA debt: web mutation audit, Content-Length boundary test

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** –   · **Parent:** T-089 (C8)

## Goal
Close the open QA points from slice 2 before new web work lands.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-098.md`; ask pl-lead for numbers (next free: D-263)

## Acceptance criteria
- [x] Web mutation audit: mutate the key web logic (stores, API client, form checks), list survivors, add tests for the ones that matter; report the rest in `docs/tasks/T-098.log.md`
- [x] Boundary test: a fetch with Content-Length == cap passes, cap+1 is refused
- [x] Small diff (aim ≤ ~150 lines); commit before each mutant (see memory rule)
- [x] `make lint test build` passes

## Out of scope
- Product code changes; a bug found goes to pl-lead as a finding

## Plan (architect, optional)

## Result (owner, max 8 lines)
pl-codex-qa/T-098-qa-debt · see PR commits · `make lint test build` · 9 focused mutants killed; three regression gaps closed; exact-cap fetch passes. Audit: `docs/tasks/T-098.log.md`.

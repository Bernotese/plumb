# T-096 – Web: Blocks screen

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** – (uses the existing proposal API)   · **Parent:** T-089 (C6)

## Goal
The user can list, view, create and edit Blocks in the web UI.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-096.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [ ] List, view, new, edit; the Block is YAML text (D-261)
- [ ] Checked against `schema/block.json` in the browser and on the server; field errors are shown at the line
- [ ] Save is a normal proposal; the Plan lists every Target the change touches
- [ ] Accessible (keyboard, labels), light and dark; `browser-shot` screenshots in the PR
- [ ] `make lint test build` and the web e2e tests pass

## Out of scope
- Agent version on the Target view (T-097)
- A form editor per resource

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

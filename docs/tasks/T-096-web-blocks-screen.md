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
- Branch `pl-frontend/T-096-web-blocks-screen`. Routes `#/blocks`, `#/blocks/new`, `#/blocks/<name>`, `#/blocks/<name>/edit`; nav "Blocks". Decisions D-263 … D-265 in `docs/decisions/T-096.md`.
- Editor: YAML text with line gutter; browser check (YAML + block.json) at the line, live after the first blur; server 422 at the line, also render errors of the Targets that use the Block. Save = `POST /plans` with the Block; the editor and the page list the Targets/Images that use it.
- Tests: `make lint test build`; vitest (blockCheck, blockYaml, blocks, screens/blocks); Go `TestBlockCheckCases` (shared cases with the browser check); e2e `slice3-blocks.spec.ts` (real server: browser error, server error at the line, Plan lists every user Target, run); full e2e 11/11.
- Screenshots: `docs/screenshots/T-096-*.png` (light, dark, 390 px, errors).
- Found: the server checks resource attributes only through a Target render. A Block that no Target uses is saved with a bad attribute (for example `mode: 0640` unquoted) and fails later at the first use. Proposal: check Block resources without templates against the type schema at save (pl-backend).

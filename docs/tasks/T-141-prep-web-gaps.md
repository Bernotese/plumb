# T-141 – Prep: gap list BRIEF §5.2 against the web UI

- **Owner:** pl-frontend   · **Reviewer:** pl-architect (uses it in T-138)   · **QA:** –
- **Size:** S–M (plan file only)   · **Depends on:** –

## Goal
Know exactly what the web UI still lacks against the brief, before the next slices are cut.

## Context (read only these)
- `docs/BRIEF.md` §5.2, §5.3 (history, rollback, export/import), §5.7 (own Blocks in the UI), §5.8 · `docs/DECISIONS.md` D-326
- `web/` routes and `api/openapi.yaml`

## Acceptance criteria
- [ ] `docs/plans/web-gaps.md` on your branch, a table: brief line · state (done / partly / missing) · where (route, component) · what is missing · needs new API (yes/no) · size. Include: settings, API tokens page, users, history with diffs and rollback, raw editing, Block create/edit, parameter forms, issue inbox actions.
- [ ] Check with the running app (`plumb serve --dev --dev-demo` + `browser-shot`), not only by reading code. Note every place where the UI shows an API feature only half.
- [ ] Push the branch, no PR. Report to `pl-lead`.

## Out of scope
- Code changes.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-006 – Web UI for the slice (mock API)

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (Claude)   · **Depends on:** T-017   · **Parallel with:** T-010, T-011
- **Status:** review

## Goal
The user can see targets, create a target from an image and Blocks, read a plan, approve it, watch the run and handle an issue. This card works against a mock of the API.

## Context (read only these)
- `docs/ARCHITECTURE.md` §3, §4, §9
- `api/openapi.yaml`, `web/` (from T-017)
- `docs/BRIEF.md` §5.2, §5.8

## Acceptance criteria
- [ ] Screens: login with forced password change · target list with status (in plumb, out of plumb, applying, offline) · new target · plan preview · run view · issue inbox · issue detail.
- [ ] New target: choose image, guest settings and Blocks. A form is built from the JSON Schema of each Block: string, number, boolean, enum, multi-line text, list of strings, default, required.
- [ ] Plan preview: changes per target, one risk badge per change (restarts are visible), reason field, approve button. A stale plan shows a clear message.
- [ ] Run view: live steps from the event stream. Issue detail: diff, `Accept`, `Revert`. A sensitive attribute shows "changed (hidden)" and has no `Accept`. Create-only fields are read-only when the user edits an existing target.
- [ ] The mock uses the examples of `api/openapi.yaml`. API types are generated, not hand-written.
- [ ] Keyboard use works on every screen. Every input has a label. Light and dark theme.
- [ ] The PR has `browser-shot` screenshots of every screen (light and dark).
- [ ] Tests: `npm test` and `npm run lint` pass in `web/`.

## Out of scope
- Real API calls (T-014).
- History, settings, tokens, Block editor.

## Plan (architect, optional)

## Result (owner, max 8 lines)
- Branch `pl-frontend/T-006-web-ui-slice`. Screens in `web/src/screens/` (login, forced password change, targets, new/edit target, plan, run, issues, issue); shared parts in `web/src/ui/`; logic in `web/src/lib/` (`client.ts` interface for T-014, `mock/`, `schemaForm.ts`, `router.ts`, `view.ts`, `theme.ts`).
- Mock built from the contract examples (`npm run gen` also writes `examples.gen.json`); it renders Blocks, computes plans (restart, risk, create-only refusal), marks stale plans, simulates runs with events, resolves issues. Examples added to `api/openapi.yaml`: more targets (all four states), `web-app` Block (every widget), sensitive issue.
- Guest form comes from `internal/state/schema/target.json` (D-043); create-only fields are read-only when editing. Multi-line text rule: D-056.
- Keyboard: native controls only, skip link, focus moves to the page title on every route, labels on every input, live region for run steps. Light, dark and system theme.
- Tests: `npm run lint` and `npm test` (58) pass; `make lint test build` pass. Screenshots of every screen, light and dark, linked in the PR.
- Live run screenshot: `browser-shot --click` times out when the click navigates; live steps are covered by the mock test (event order, final states).

# T-006 – Web UI for the slice (mock API)

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (Claude)   · **Depends on:** T-017   · **Parallel with:** T-010, T-011
- **Status:** proposed (do not start before the user approves the architecture)

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
- [ ] Run view: live steps from the event stream. Issue detail: diff, `Accept`, `Revert`. A sensitive attribute shows "changed (hidden)" and has no `Accept`.
- [ ] The mock uses the examples of `api/openapi.yaml`. API types are generated, not hand-written.
- [ ] Keyboard use works on every screen. Every input has a label. Light and dark theme.
- [ ] The PR has `browser-shot` screenshots of every screen (light and dark).
- [ ] Tests: `npm test` and `npm run lint` pass in `web/`.

## Out of scope
- Real API calls (T-014).
- History, settings, tokens, Block editor.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

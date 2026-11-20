# T-131 – Screenshots for the README

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-118 (PR #227; take the pictures of main after it is merged)

## Goal
The README shows the tool in 5 to 7 pictures along the user's path (user order, 2026-11-13).

## Context (read only these)
- `README.md`; the tool `browser-shot` (AGENTS.md "Tools on this machine")

## Acceptance criteria
- [ ] Pictures made with `browser-shot` of `plumb serve --dev --dev-demo` on main. **Never of the test host**: no real address, token, host name or key in a picture. Demo data only; check that the demo data itself shows no real address or name
- [ ] 5 to 7 pictures: Targets overview · one Target · Plan preview with approval · a Run with live progress · Issues / drift · Images · Blocks screen
- [ ] Files `docs/screenshots/readme-*.png`; PNG; aim below 300 KB each (fitting viewport, no `--full` where it is not needed, lossless optimise); one theme for all (light or dark)
- [ ] In the README: each picture in its section of the user's path, with one line of caption and an alt text; no development docs
- [ ] The PR lists each file with size and what it shows. pl-reviewer and pl-lead look at every picture for sensitive data before the merge
- [ ] `make lint` green

## Out of scope
- Changes to the UI or the demo data (tell pl-lead if the demo data is not fit for a picture)

## Result (owner, max 8 lines)
branch · commit · tests · notes

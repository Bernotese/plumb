# T-024 – `serve --dev`: simulated agent for new targets

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-011, T-023 (merged)

## Goal
In `plumb serve --dev`, a target the user creates in the UI can be applied. Today only the seeded targets `web1`/`web2` get a simulated agent, so the Run of a new target stays "running".

## Context (read only these)
- `cmd/plumb` serve `--dev` (`seedDev`, simulated agents).
- Report of pl-frontend in PR #28 (T-014).

## Acceptance criteria
- [ ] In `--dev`, every target created through the API gets a simulated agent (enrolls, applies, reports). Its Run finishes.
- [ ] Deleting the target stops its simulated agent. No goroutine leak (test).
- [ ] Nothing of this exists outside `--dev`.
- [ ] Tests: `make lint test build` passes; one test creates a target through the API in dev mode and waits for the Run to succeed.

## Out of scope
- Simulated drift for new targets.

## Result (owner, max 8 lines)
branch · commit · tests · notes

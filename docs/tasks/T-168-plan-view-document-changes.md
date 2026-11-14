# T-168 – Plan screen: show the document changes of a Plan without Targets (host finding F1)

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
A Block-only Plan (e.g. "Update from the catalogue" for a Block that no Target uses) shows nothing on the Plan screen, so a person approves it blind. The Plan screen must show what the Plan changes in the documents.

## Context (read only these)
- `docs/tasks/T-158.log.md` (PR #265; finding F1)
- `web/src/screens/PlanView.svelte` · `api/openapi.yaml` (Plan, proposal, revision diff) · roadmap row 18 (the full version is slice 8; this card is the small part: no blind approval)

## Acceptance criteria
- [ ] For every Plan the screen lists the documents the Plan changes (kind, name, added / changed / removed) and for a changed document a diff of its text, masked like everywhere else (no secret values).
- [ ] First check whether the API gives this for a **pending** Plan. If not, stop and report to `pl-lead` which field is missing (the server part then goes to pl-backend); do not rebuild the diff from two unrelated calls if that can show something else than what will be applied.
- [ ] e2e: the catalogue update of a Block without Targets shows the diff before approval. `browser-shot` picture from the mock/demo only.
- [ ] `make lint test` passes; own worktree and branch; PR to `main`; report to `pl-lead`.

## Out of scope
- Anything else of the log.

## Result (owner, max 8 lines)
branch · commit · tests · notes

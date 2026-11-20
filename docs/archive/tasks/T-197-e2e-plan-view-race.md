# T-197 – e2e: wait for the Plan content, not only its heading

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
PR #274 was red in e2e (job 132330637955): `web/e2e/slice3-blocks.spec.ts:62` read the Plan screen before `getPlan` returned (the `h1` renders first; `allTextContents()` does not wait). `GET /plans/{id}` is slower since T-170, so the window is wider. A test fault, proven by pl-frontend from the log.

## Acceptance criteria
- [ ] `slice3-blocks.spec.ts` and `slice5-gaps.spec.ts:168` wait for the Target headings (or the document section) before they read. Sweep the other specs for the same pattern (`allTextContents` / `count()` right after a heading wait) and fix them the same way; list them in the PR.
- [ ] Also say whether `GET /plans/{id}` got slow enough to matter for a user (time it in the demo with and without `documents`); if yes, tell `pl-lead` (server card).
- [ ] `make lint test` + e2e pass; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

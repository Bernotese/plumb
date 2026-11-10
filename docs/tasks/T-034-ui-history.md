# T-034 – UI: revision history, diff, rollback

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-027 (mock first); e2e part needs T-031 on `main`

## Goal
A user browses revisions, sees what changed and starts a rollback that opens the normal Plan view.

## Context (read only these)
- `docs/BRIEF.md` §5.2, §5.3 · `docs/DECISIONS.md` D-093 · `api/openapi.yaml` – `/revisions`
- `web/src/App.svelte`, `web/src/lib/router.ts`, `web/src/screens/PlanView.svelte`, `web/src/ui/ChangeTable.svelte`

## Files
`web/src/screens/History.svelte`, `RevisionView.svelte` · `web/src/ui/DocDiff.svelte` · `web/src/lib/textDiff.ts` (+ test) · router, nav, client · `web/e2e/*`

## Acceptance criteria
- [ ] The card adds its operations to `PlumbApi` (`web/src/lib/client.ts`), the HTTP client and the mock. The mock data comes from the contract examples (`example(op, code)`).
- [ ] Nav entry "History". List: number, time, author, reason, link to the Plan. Paged.
- [ ] Revision view: per document `added`, `changed`, `removed`; a changed document shows a line diff of its YAML text. The diff function is own code with unit tests (no new dependency, D-035).
- [ ] "Roll back to this revision" asks for a reason, calls the API and opens the Plan view. 409 `nothing-to-do` shows a clear message.
- [ ] Keyboard use, visible focus, colour is not the only sign for added and removed lines. Light and dark.
- [ ] Vitest for the screens on the mock. Playwright on `plumb serve --dev`: change a target twice, open the diff, roll back, approve.
- [ ] `browser-shot` pictures (light, dark, narrow) in the PR. `make lint test` passes.
- [ ] Stays inside approved decisions.

## Out of scope
- Diff of secret values (none exist in documents). Editing a revision.

## Result (owner, max 8 lines)
- Mock part done: branch `pl-frontend/T-034-ui-history`. Screens History, RevisionView, DocDiff; `listRevisions`, `diffRevision`, `rollbackRevision` in PlumbApi, HTTP and mock (mock data from the contract examples; approve commits revision snapshots).
- Own YAML writer + line diff with unit tests (D-110, D-111). Screen tests in happy-dom (D-112). Paging + live reload (D-113).
- Tests: `make lint test` → pass (117 web tests). Pictures: branch `pl-frontend/T-034-screens`, in the PR.
- Open: Playwright e2e waits for T-031 on `main`.

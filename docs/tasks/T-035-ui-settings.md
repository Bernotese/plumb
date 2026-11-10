# T-035 – UI: settings – API tokens, secrets, export/import

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (three small screens; split the PR per screen if it grows)   · **Depends on:** T-027 (mock first); e2e parts need T-030, T-028, T-033 on `main`

## Goal
A human manages API tokens and secrets and runs export and import in the web UI.

## Context (read only these)
- `docs/BRIEF.md` §5.2, §5.9 · `docs/DECISIONS.md` D-046, D-087, D-090, D-094, D-095 · `api/openapi.yaml` – `/tokens`, `/secrets`, `/export`, `/import`
- `web/src/screens/TargetForm.svelte` (form style), `web/src/ui/ProblemBox.svelte`

## Files
`web/src/screens/Settings.svelte`, `Tokens.svelte`, `Secrets.svelte`, `ExportImport.svelte` · router, nav, client · `web/e2e/*`

## Acceptance criteria
- [ ] The card adds its operations to `PlumbApi` (`web/src/lib/client.ts`), the HTTP client and the mock. The mock data comes from the contract examples (`example(op, code)`).
- [ ] **Tokens:** list (name, tier, max risk, created, last used, revoked). Create form: `max_risk` only for tier `apply`. The bearer is shown once with a copy button and a "you cannot see it again" note; it is not kept in any store or URL. Revoke asks for confirmation. A note says that an `apply` token is root on the guests (§7).
- [ ] **Secrets:** list (name, version, changed by, changed at, used by). Set and replace with a password field (`autocomplete="off"`; multi-line switch for keys). A value is never shown again and never kept after the request. Delete is disabled with a reason while targets use the secret.
- [ ] After a replace with `used_by` targets, the screen offers "Plan the change for N targets" (`Proposal.targets`) and opens the Plan view (D-090).
- [ ] **Export:** download without secrets; with secrets after a passphrase (typed twice, ≥ 12 characters). **Import:** file choice, mode `replace` or `merge` with a plain explanation, optional passphrase, reason; the result opens the Plan view. Missing secrets are listed with a link to the secrets screen. On 409 `secret-conflict` the screen lists the names; the user ticks each secret to overwrite and sends again (`overwrite`). Nothing is ticked by default.
- [ ] A token session cannot reach these screens; a 403 shows the problem text.
- [ ] Vitest on the mock per screen. Playwright on `plumb serve --dev`: create and revoke a token; set a secret; export, import, see the Plan.
- [ ] `browser-shot` pictures (light, dark, narrow) per screen in the PR. `make lint test` passes.
- [ ] Stays inside approved decisions.

## Out of scope
- User management, password policy, scheduled backups.

## Result (owner, max 8 lines)
- Mock part done: branch `pl-frontend/T-035-ui-settings` (from `main` after T-034). Settings tabs Tokens, Secrets, Export and import; 8 operations in PlumbApi, HTTP and mock (D-120…D-124).
- Tests: `make lint test` → pass (167 web tests; 18 new screen tests in `screens/settings.test.ts`, HTTP calls, router).
- Pictures: branch `pl-frontend/T-035-screens`, in the PR.
- E2E tokens and secrets done (branch `pl-frontend/T-034-T-035-e2e`, `web/e2e/slice2-settings.spec.ts`): token create → bearer works → revoke → 401; secret set, replace, delete, no value in API or page. Open: export/import e2e waits for T-033.

# T-036 – UI: secret parameters and ignore actions

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-027 (mock first), T-035 (secrets screen); e2e parts need T-029, T-032 on `main`

## Goal
The target form takes secrets for secret parameters. The issue view offers "Ignore once" and "Ignore by rule". A user can see and remove rules.

## Context (read only these)
- `docs/DECISIONS.md` D-056, D-088, D-089, D-091, D-092 · `api/openapi.yaml` – `resolveIssue`, `/rules`, `TargetPlan.secrets`, `Change.ignored`
- `web/src/lib/schemaForm.ts`, `web/src/ui/SchemaFields.svelte`, `web/src/screens/IssueView.svelte`, `PlanView.svelte`, `web/src/ui/ChangeTable.svelte`

## Files
`web/src/lib/schemaForm.ts` (+ test) · `web/src/ui/SchemaFields.svelte`, `SecretPicker.svelte` · `web/src/screens/IssueView.svelte`, `Issues.svelte`, `Rules.svelte`, `PlanView.svelte` · `web/e2e/*`

## Acceptance criteria
- [ ] A `writeOnly` string parameter renders as a secret picker: choose an existing secret or create one in place. The form saves `{secret: name}` and never a literal (D-088).
- [ ] The Plan view shows "uses secrets: …" per target and shows a sensitive change as "changed (hidden)". It marks a change with `ignored: true` as "a rule ignores this drift; this apply overwrites it".
- [ ] Issue view: "Ignore once" (reason, then the Issue is resolved) and "Ignore by rule" (this target or all targets; all attributes or the changed ones; opens the Plan view). Buttons follow `Issue.actions`.
- [ ] Rules list under Issues: target, resource, attributes, revision. "Remove" creates a proposal and opens the Plan view.
- [ ] The issue list can show resolved Issues with their resolution, so an ignored Issue can be found again.
- [ ] Vitest on the mock. Playwright on `plumb serve --dev`: target with a secret parameter; ignore once; ignore by rule and remove the rule.
- [ ] `browser-shot` pictures (light, dark, narrow) in the PR. `make lint test` passes.
- [ ] Stays inside approved decisions.

## Out of scope
- Block editor (declaring a secret parameter in the UI). Raw mode.

## Result (owner, max 8 lines)
branch · commit · tests · notes

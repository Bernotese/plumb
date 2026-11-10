# T-027 – Slice 2 API contract

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** T-025 approved by the user

## Goal
`api/openapi.yaml` describes every part A operation of slice 2. Backend and UI cards then run in parallel.

## Context (read only these)
- `docs/ARCHITECTURE.md` §12, `docs/DECISIONS.md` D-087 … D-096, D-099
- `api/openapi.yaml` (style: `/issues` operations), `web/scripts/check-openapi.mjs`

## Contract (this table is the source until the YAML exists)
| Operation | Minimum tier | Shape and errors |
|---|---|---|
| `GET /tokens` | session | Page of `Token` `{id, name, tier, max_risk, created_by, created_at, last_used_at, revoked_at}` |
| `POST /tokens` `{name, tier, max_risk?}` | session | 201 `{token, bearer}`; the bearer is shown once; `Cache-Control: no-store`. 422 for a pair that D-046 forbids |
| `DELETE /tokens/{id}` | session | 204, soft revoke. 404 |
| `GET /revisions` | read | Page of `Revision` `{id, author, reason, plan_id, created_at, documents}` (count) |
| `GET /revisions/{n}/diff?from=<m>` | read | `{from, to, changes: [{kind, name, op, before, after}]}`; `op` = `added`, `changed`, `removed` |
| `POST /revisions/{n}/rollback` `{reason}` | plan | 201 Plan, origin `rollback`. 409 `nothing-to-do` when `n` equals the head set |
| `GET /secrets` | read | Page of `SecretInfo` `{name, version, updated_by, updated_at, used_by}`; never a value |
| `PUT /secrets/{name}` `{value}` | session | 200 `SecretInfo`. `value` is `writeOnly` |
| `DELETE /secrets/{name}` | session | 204. 409 `secret-in-use` with the target names and the ids of pending Plans |
| `GET /rules`, `GET /rules/{name}` | read | Like `/blocks`. Schema: new file `internal/state/schema/rule.json` (D-092) |
| `POST /issues/{id}/resolve` | plan | `IssueAction` adds `ignore_once` (200 Issue; human session, or `apply` token per D-091, else 403 `human-required` / `risk-too-high`) and `ignore_rule` (201 Plan; body adds `rule: {all_targets?, attrs?}`) |
| `GET /export` | read | `application/yaml`, no secret values; `Cache-Control: no-store` |
| `POST /export` `{passphrase}` | session | `application/yaml` with the secret bundle; `Cache-Control: no-store` |
| `POST /import` `{content, mode, passphrase?, overwrite?, reason}` | session | 201 Plan, origin `import`. 422 lists missing secrets. 409 `secret-conflict` lists the names that exist with another value and are not in `overwrite` |

Schema changes: `Proposal.targets`; `TargetPlan.secrets` (`[{name, version}]`, D-089); `Plan.human_only` (list of `secret-binding`, `rule-change`; empty for a normal Plan; a token that approves gets 403 `human-required`, D-099); `Change.ignored` (a rule ignores this drift, D-092); target status adds `ignored_drift` (`[{resource, rule, attrs, last_seen}]`, D-092); Plan origin adds `rollback`, `import`, `ignore_rule`; Issue resolution adds `ignore`; event types add `token.created`, `token.revoked`, `secret.updated`, `secret.deleted`; problem codes add `secret-in-use`, `secret-conflict`, `nothing-to-do`.

## Files
`api/openapi.yaml` · `internal/state/schema/rule.json` · generated: `web/src/lib/api.gen.ts`, `web/src/lib/mock/examples.ts` · `web/src/lib/mock/mock.ts` only if the mock needs a route entry

## Acceptance criteria
- [x] All rows and schema changes above are in `api/openapi.yaml`, with tags, security, examples and problem responses in the existing style.
- [x] No example holds a secret value or a marker character. `PUT /secrets` and `POST /export` mark `value` and `passphrase` as `writeOnly`, `format: password`.
- [x] `rule.json` is closed (`additionalProperties: false`) and the contract refers to it (D-043). `state` does not load it yet (T-032).
- [x] The texts that say "ignore actions come later" (D-044) are updated.
- [x] Generated files are regenerated (`npm run gen`; they are not in git). The examples of the new operations are ready for the mock (`example(op, code)`). The mock is a typed `PlumbApi` object, not a route table, so each UI card adds its own methods (noted in T-034 … T-036).
- [x] `make lint test` passes. No Go behaviour changes; a handler for a new route is not part of this card.
- [x] Operations without a handler carry `x-plumb-planned: <card>`; the tier matrix test knows the mark (D-100).
- [x] Needs no new architecture decision. A shape that must differ from the table gets one line in `docs/DECISIONS.md` (from D-100).

## Out of scope
- Handlers, store code, UI screens. Part B operations (image versions, builds).

## Result (owner, max 8 lines)
pl-architect/T-027-slice-2-contract · draft PR (waits for the user's approval of T-025) · tests: `make lint test` → ok (Go all packages, web 88 tests).
- 14 new operations, 5 new tags, `rule.json`. All new operations carry `x-plumb-planned` (D-100), because a QA test wants a tier row for every operation.
- New fields on existing schemas are optional ("missing = none"), so the slice 1 server still matches the contract.
- Test-only Go change: 8 lines in `internal/api/access_qa_test.go`. Web: 2 lines in the mock (it refuses the two new issue actions until T-036).
- Open questions of T-025 are marked in the YAML with `T-025 question 1` (`IgnoredFlag`) and `T-025 question 3` (`resolveIssue`).

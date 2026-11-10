# T-029 – Secret references: render, delivery, redaction

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** T-028

## Goal
A Block parameter can take a secret. The value reaches the agent and nothing else. Plans, Issues, events, logs and API responses never show it.

## Context (read only these)
- `docs/ARCHITECTURE.md` §7, §12 · `docs/DECISIONS.md` D-037, D-047, D-062, D-069, D-088 … D-090
- `internal/state/render.go`, `template.go`, `validate.go` · `internal/plan/propose.go`, `plan.go`, `run.go` · `internal/link/link.go` (Send, masking) · `internal/api/views.go`

## Files
`internal/state/*` (secret parameter, marker, opaque template value, `SecretNames`, `WithSecrets`) · `internal/plan/*` (resolve in command bodies, missing secret, `TargetPlan.Secrets`, `Proposal.Targets` in the API) · `internal/api/*` (`used_by`, 409 `secret-in-use`, views) · `blocks/` (one starter Block gets a secret parameter, for example an env file) · tests

## Acceptance criteria
- [ ] Reference rules of D-088: `writeOnly` string parameter ↔ `{secret: name}`; literal refused; reference on another parameter refused; marker characters in a document refused; marker in a key attribute, `restart_on` or `guest:config` refused. Each error has a JSON pointer.
- [ ] The template value is opaque: a table test calls every sandbox function of D-052 with a secret parameter and gets an error or the unchanged marker. No function returns a part, the length or a comparison result.
- [ ] `Resource.Sensitive` lists every attribute with a marker. `plan.Engine.Sensitive` returns it without a code change in `link` (D-090).
- [ ] `plan` replaces markers only in `observe`, `apply` and `watch` bodies (D-089). The stored proposal, Plan, run steps and the render cache hold no value.
- [ ] A new target plans a sensitive attribute as `{attr, sensitive, risk}` without `to`. `TargetPlan.secrets` lists the names.
- [ ] A missing secret is 422 `validation` with the pointer of the parameter. `DELETE /secrets/{name}` is 409 while the head revision uses it. `GET /secrets` fills `used_by`.
- [ ] `PUT /secrets/{name}` changes no guest. A Plan with `targets: [used_by…]` shows a sensitive change and applies the new value (slice test with `linktest`).
- [ ] No API response contains U+E000 or U+E001: views show the attribute as sensitive.
- [ ] Stays inside approved decisions (D-088 … D-090).

## Tests
- Slice test with a canary value: create target with a secret, apply, change the secret, plan, apply, drift on the sensitive attribute, revert. Then search the database file, all events, all API responses of the test and the captured log for the canary: zero hits.
- `linktest` agent with `LeakSensitive`: the controller still stores no value.
- `make lint test` passes.

## Out of scope
- Secrets in `guest:config` (cloud-init user password), in overrides and in Image documents. UI (T-035, T-036).

## Result (owner, max 8 lines)
branch · commit · tests · notes

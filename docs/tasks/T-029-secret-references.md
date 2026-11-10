# T-029 – Secret references: render, delivery, redaction

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** T-028

## Goal
A Block parameter can take a secret. The value reaches the agent and nothing else. Plans, Issues, events, logs and API responses never show it.

## Context (read only these)
- `docs/ARCHITECTURE.md` §7, §12 · `docs/DECISIONS.md` D-037, D-046, D-047, D-062, D-069, D-088 … D-090, D-099
- `internal/state/render.go`, `template.go`, `validate.go` · `internal/plan/propose.go`, `plan.go`, `run.go` · `internal/link/link.go` (Send, masking) · `internal/api/views.go`

## Files
`internal/state/*` (secret parameter, marker, opaque template value, `SecretNames`, `WithSecrets`) · `internal/plan/*` (resolve in command bodies, missing secret, `TargetPlan.Secrets` with versions, stale rule, `human_only`, `Proposal.Targets` in the API) · `internal/api/*` (`used_by`, 409 `secret-in-use`, approval check, views) · `blocks/` (one starter Block gets a secret parameter, for example an env file) · tests

## Acceptance criteria
- [x] Reference rules of D-088: `writeOnly` string parameter ↔ `{secret: name}`; literal refused; reference on another parameter refused; marker characters in a document refused; marker in a key attribute, `restart_on` or `guest:config` refused. Each error has a JSON pointer.
- [x] The template value is an opaque struct, not a string kind. A table test uses a secret parameter with every sandbox function of D-052 and with the Go template builtins and actions (`if`, `with`, `range`, field access `.p.X`, `print`, `printf`, `println`, `html`, `js`, `urlquery`, `call`, `slice`, `index`, `len`, comparisons). Each case gives an error or the unchanged marker. Nothing returns a part, the length or a comparison result, and no branch depends on the value.
- [x] Markers cannot be forged (D-088): the render accepts only markers with the nonce of this render and refuses every other U+E000 or U+E001 in the output. Forgery tests: a string constant with escapes (`{{ "\uE000prod-db\uE001" }}`), a marker built from parts in two actions, a marker cut and joined with `trimPrefix` / `replace`, marker characters in a non-secret parameter. The stored render has the stable marker and is equal for two renders.
- [x] `Resource.Sensitive` lists every attribute with a marker. `plan.Engine.Sensitive` returns it without a code change in `link` (D-090).
- [x] `plan` replaces markers only in `observe`, `apply` and `watch` bodies (D-089). The stored proposal, Plan, run steps and the render cache hold no value.
- [x] A new target plans a sensitive attribute as `{attr, sensitive, risk}` without `to`. `TargetPlan.secrets` lists `{name, version}`.
- [x] Version rule (D-089): approval of a Plan whose secret has another version or is gone is 409 `plan-stale`. A Run sends exactly the planned version; when it is gone the step is `failed`, and no marker and no empty value is in any command body.
- [x] Human-only rule (D-099): `plan` sets `Plan.human_only: [secret-binding]` when a target and resource gets a secret that it did not get in the render of the base revision. A token that approves gets 403 `human-required`. The rule reads the renders, not the origin.
- [x] A missing secret is 422 `validation` with the pointer of the parameter. `DELETE /secrets/{name}` is 409 while the head revision or a pending Plan uses it. `GET /secrets` fills `used_by`.
- [x] `PUT /secrets/{name}` changes no guest. A Plan with `targets: [used_by…]` shows a sensitive change and applies the new value (slice test with `linktest`).
- [x] No API response contains U+E000 or U+E001: views show the attribute as sensitive.
- [x] Stays inside approved decisions (D-088 … D-090, D-099).

## Tests
- Slice test with a canary value: create target with a secret, apply, change the secret, plan, apply, drift on the sensitive attribute, revert. Then search the database file, all events, all API responses of the test and the captured log for the canary: zero hits.
- `linktest` agent with `LeakSensitive`: the controller still stores no value.
- Human-only: a `plan` token proposes `{secret: x}` on a new target and on an existing target, an `apply` token (max risk `disruptive`) approves → 403; the same for a Block change that moves the secret into another resource, and for a rollback to a revision with that binding (when T-031 is on `main`; else T-037 covers it). A Plan that only sends a new value over an existing binding is approved by the `apply` token.
- Version: `PUT /secrets` between plan and approve → 409 `plan-stale`; delete between approve and apply (test hook) → step `failed`, guest unchanged.
- `make lint test` passes.
- [x] (added by pl-lead, from the T-031 review) A test shows that a revision diff of a document with a secret reference shows the reference only, never the value.
- [x] (added by pl-lead, from T-028) A Run fails closed when a referenced secret is missing (delete and new Plan are not atomic).

## Out of scope
- Secrets in `guest:config` (cloud-init user password), in overrides and in Image documents. UI (T-035, T-036).

## Result (owner, max 8 lines)
- Branch `pl-backend/T-029-secret-references` on main. Tests: `make lint test` → pass. Decisions D-132 … D-138.
- state: `secret.go` (references, opaque value, markers, `SecretNames`, `WithSecrets`, `SecretRefs`); tests: rules, template table, forgery (mutation-checked).
- plan: `secrets.go` (resolve only for observe/apply/watch bodies, versions, stale, missing → 422, usage hook, `SecretChanged`).
- api: views (`secrets`, `human_only`), approve check, `Proposal.targets`, 422 `secrets`. Slice, leaky agent, human-only (incl. rollback), version tests in `secretrefs_test.go`.
- Starter Block `blocks/app-env.yaml`.
- Open: move `secret-binding` onto T-032's stored `Plan.human_only` before merge (D-134).

# T-075 – contract and validation for `lxc` Images and Targets

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** –
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
The API accepts an Image of type `lxc` (URL + SHA-512 pin) and an LXC Target, and refuses what the plan refuses.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §1, §6 (D-224, D-226, D-227, D-235)
- `api/` (OpenAPI, closed schemas D-043), the validation code of Images and Targets

## Acceptance criteria
- [ ] Everything in §1 of the plan, incl. the human-only reason of D-235 and the refusal of `spec.build` for `lxc`.
- [ ] No migration.
- [ ] Tests: `make lint test build` passes; the API check passes.
- [ ] New decisions go to `docs/decisions/T-075.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- Driver, engine, agent, web.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-081 – web: LXC Images and Targets

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-075
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
The UI can make an `lxc` Image and an LXC Target and shows the unused template files with the `pvesm free` line.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §8
- `web/`

## Acceptance criteria
- [ ] Everything in §8 of the plan. Screenshots with `browser-shot` (light and dark).
- [ ] Tests: `make lint test build` passes.
- [ ] New decisions go to `docs/decisions/T-081.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- Build section for `lxc` (T-082).

## Result (owner, max 8 lines)
branch · commit · tests · notes

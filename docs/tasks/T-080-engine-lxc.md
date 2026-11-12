# T-080 – engine: LXC create, clear, re-enroll, delete, cancel

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-075
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
A Plan for an LXC Target creates the container, waits for the hello, clears the token and handles re-enrollment, delete and cancel.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §4 (D-231, D-232, D-233)
- `internal/plan`

## Acceptance criteria
- [ ] Everything in §4 of the plan, against the mock driver.
- [ ] Tests: `make lint test build` passes.
- [ ] New decisions go to `docs/decisions/T-080.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- The Proxmox driver, image builds.

## Result (owner, max 8 lines)
`pl-frontend/T-080-engine-lxc` (on T-075) · `make lint test build` → ok · tests `internal/issue/lxc_flow_test.go` (create, clear fails, no hello → re-enroll, re-enroll, live resize, delete, cancel during fetch, leak scan of DB + log), `restart_test.go` for the LXC rule.
- Driver interface: `ImageSpec.SHA512`, `GuestState.TokenInConfig`, `ClearBootstrap(ctx, id)`. Proxmox has a stub that refuses (T-079 implements it and must set `TokenInConfig`). driver/fake models containers (env, live resize, type match).
- `TextReenrollRefused` and the restart error still say "VM"; T-081 may want container wording.

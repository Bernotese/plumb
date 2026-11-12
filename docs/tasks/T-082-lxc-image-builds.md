# T-082 – LXC image builds

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-079, T-080
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
An `lxc` Image can have a build recipe; no enrollment token can reach a template.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §5 (`seal`), §11 (row T-082), D-234

## Acceptance criteria
- [ ] D-234 and review S8: no `env` and no pending `env` before the template call; a clone config holds no inherited `env`; `ssh-keygen` before ssh.
- [ ] Tests: `make lint test build` passes.
- [ ] New decisions go to `docs/decisions/T-082.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- –

## Result (owner, max 8 lines)
branch · commit · tests · notes

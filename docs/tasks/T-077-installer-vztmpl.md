# T-077 – installer and start check: content `vztmpl`, upgrade order

- **Owner:** pl-reviewer (pl-codex-fix: not trivial)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** –
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
A new install and a repair run give `plumb-images` the content `import,vztmpl`; the start check accepts both sets; a failed upgrade rolls back in the safe order.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §7 (D-229)
- `install/install.sh`, `install/install_test.go`, the start check of D-183 in the driver, the installer docs

## Acceptance criteria
- [ ] Everything in §7 of the plan: plan line and question (D-084), order binary → content → restart, rollback order (S9), dry-run text.
- [ ] The start check accepts exactly `{import}` (warning) or `{import, vztmpl}`, as sets.
- [ ] Docs: the upgrade paragraph and the rollback line.
- [ ] Tests: `make lint test build` passes. No run on the real host (pl-backend does that later).
- [ ] New decisions go to `docs/decisions/T-077.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- `Sys.Audit` (T-084 changes the roles; do not touch `ROLE_PRIVS`). The real host.

## Result (owner, max 8 lines)
branch · commit · tests · notes

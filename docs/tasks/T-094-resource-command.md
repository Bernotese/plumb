# T-094 – Resource command (always destructive)

- **Owner:** pl-architect (after T-090)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-090   · **Parent:** T-089 (C4b)

## Goal
An escape hatch resource that only a person can approve and that never runs in an Image build.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-094.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [x] `command` with a `creates` guard (runs once), no drift (D-260)
- [x] Always `destructive` by the existing D-031 rule: only a human session approves; no new rule
- [x] An Image whose build Blocks hold a `command` is refused at save
- [x] Reviewer note: the refusal also catches a Block that gets a `command` AFTER an Image uses it, and the build checks again at start
- [x] Command output is not logged in clear when it may hold secrets (follow the existing log rules)
- [x] Tests for all refusals; `make lint test build` passes

## Out of scope
- Any way for a token or `system` to approve it

## Plan (architect, optional)

## Result (owner, max 8 lines)
pl-architect/T-094-resource-command (on top of T-090, PR #187) · `make lint test build` ok.
`resource.command` (no shell, `creates` guard, no stdout/stderr in Output), type flags NoDrift/Destructive/NotInImage; agent drift check skips it; `state.PlanRisk` makes every command change destructive in a Plan (also unverified, also if the agent reports less); render refuses it in an Image build (save, later Block change, build start). Tests: resource, state, api (token refused), agent (no drift); mutants of PlanRisk (2 paths) and the drift skip are caught.

# T-092 – Controller: agent version, update step, version gate

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-090, T-091   · **Parent:** T-089 (C3)

## Goal
The controller knows each agent's version and capabilities, proposes an "update agent" Plan step, and refuses a Plan the agent cannot run.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-092.md`; ask pl-lead for numbers (next free: D-263)

## Acceptance criteria
- [ ] Agent version and capabilities in the Target status (API)
- [ ] `system` proposes an update step when capabilities differ; risk `disruptive`; a person or token approves (D-257)
- [ ] The step passes when a `hello` with the new version arrives within 2 min
- [ ] Gate: a Plan that needs a missing capability is refused with "update the agent first"; the Run checks again at start (D-259)
- [ ] `make lint test build` passes

## Out of scope
- Web (T-097)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

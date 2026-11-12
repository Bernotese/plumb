# T-097 – Web: agent version and update on the Target view

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-092, T-096   · **Parent:** T-089 (C7)

## Goal
The Target view shows the agent version, offers "update agent" and explains the gate refusal.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-097.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [ ] Agent version and "update available" on the Target view
- [ ] "Update agent" opens the proposed Plan (no silent action)
- [ ] The gate message "update the agent first" is shown with a link to the update
- [ ] Screenshots; web tests pass

## Out of scope
- –

## Plan (architect, optional)

## Result (owner, max 8 lines)
- Branch `pl-frontend/T-097-web-agent-version` (on T-092, PR #196).
- Agent card (version, connection, state, what the agent lacks, a revert, "Propose agent update" → the Plan) on the Target page and on `#/targets/<name>/agent`; Targets list shows `agent vX` and a badge link; a Target with `required` has a top banner; the problem box shows `agent-outdated` with what is missing and a link per Target; PlanView names origin `agent_update`.
- Mock: agent states for web1 (available), cache1 (required + revert), backup1 (manual); `updateTargetAgent`; 409 `agent-outdated` on a Plan for cache1.
- Tests: `make lint-web test-web` → ok (397); new `lib/agent.test.ts`, `screens/agent.test.ts`. Screenshots `docs/screenshots/T-097-*.png`. e2e `slice3-agent.spec.ts` (real server): the demo agents run the served binary, so it checks the "current" card and the list; the update path is covered by T-092 server tests and the mock screen tests.

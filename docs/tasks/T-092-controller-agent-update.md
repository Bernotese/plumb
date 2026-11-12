# T-092 – Controller: agent version, update step, version gate

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-090, T-091   · **Parent:** T-089 (C3)

## Goal
The controller knows each agent's version and capabilities, proposes an "update agent" Plan step, and refuses a Plan the agent cannot run.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-092.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [ ] Agent version and capabilities in the Target status (API)
- [ ] `system` proposes an update step when capabilities differ; risk `disruptive`; a person or token approves (D-257)
- [ ] The step passes when a `hello` with the new version arrives within 2 min
- [ ] Gate: a Plan that needs a missing capability is refused with "update the agent first"; the Run checks again at start (D-259)
- [ ] `make lint test build` passes

## Out of scope
- Web (T-097)

## Plan (architect, optional)

### Design (pl-backend, 2026-11-12)
- **Store** (migration 0012): `plan.origin` gets `agent_update`. `agent` gets `hello_version`, `binary_sha256`, `capabilities` (JSON; NULL = no field = baseline), `reverted` (JSON), `hello_at`; written at every hello of a bound Target after the guest-text checks of ARCHITECTURE §14. `ApproveBuildPlan` also takes `agent_update` (no revision, Run pinned to head). `agent_update` Plans do not go stale when the head moves.
- **Link:** `Send` takes `update` (`UpdateBody`, never to a `build:` link); a result may hold the Item `agent:binary` only as the answer of an outstanding `update`.
- **Engine:** `Config.Agent{SHA256, Version, Capabilities}` = the agent that this controller serves (from `cmd/plumb`). `ProposeAgentUpdate(target)`: Plan `agent_update`, one step `agent:binary` `{version: from → to}`, risk `disruptive`. The Run sends `update {plan_id, sha256}`; Item `failed` → step failed; `applied` → wait ≤ 120 s for a hello: sha = served → `applied`; `reverted` or the old sha → `failed` with the reason; none → `failed`. Then the watch list goes again.
- **system proposes** on a hello when capabilities differ, the agent lists `update`, the sha differs, there is no pending `agent_update` Plan for the Target, and `reverted.to_sha256` is not the served sha (that binary failed here already: no proposal loop).
- **Gate** (`wire.Capabilities.Missing` against the stored list): at Propose and Approve for origins other than `agent_update`, `reenroll`, `build`; a Target without a hello is not gated there. In the Run, right before the observe/apply command (after a create or enrollment, so the hello is there): missing → all steps `failed` with "update the agent first: …", no command. Watch lists leave out what the agent lacks.
- **Status/API:** `agent_version`, `agent_update` (`unknown` no hello · `none` same sha · `manual` no `update` command · `required` something missing · `available`), `agent_missing`, `agent_reverted`. `POST /targets/{name}/agent-update`: 201; 409 `nothing-to-do` for `none` or a pending `agent_update` Plan; 422 for `manual`/`unknown`. Problem `agent-outdated` with `targets`, `missing`.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-102 – Update step: the Run must see the outcome when the result is lost at the swap

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S–M   · **Depends on:** –   · **Parent:** T-089 (found in T-100, F1)

## Goal
On the real host an agent update whose command result was lost at the binary swap left the Run waiting for ApplyTimeout (15 min); it ignored the `reverted` hello and the Target went to phase error. The Run must end as soon as the outcome is known.

## Context (read only these)
- `docs/tasks/T-100.log.md` – finding F1 with the timeline
- `internal/link`, the update step code of T-092, ARCHITECTURE §14 (D-258, D-267)

## Acceptance criteria
- [ ] A hello on a new connection of that agent ends the wait of the update command: new sha = step passes; `reverted` or the old sha = step fails with a clear reason; no 15 min wait
- [ ] The Target does not end in phase error for a clean revert; `agent_reverted` is shown and D-275 holds
- [ ] Tests for: result lost + good hello, result lost + reverted hello, result lost + no hello (the 2 min rule of D-258)
- [ ] `make lint test build` passes; say if the host must re-check it

## Out of scope
- Changes to the guard in the guest

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

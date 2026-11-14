# T-210 – A park must end when the agent ran the first unbind late (host findings F12, F11)

- **Owner:** pl-reviewer (implementer; pl-architect is on T-209, pl-backend on the host)   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
Found on the real host (T-192 re-run, build f909ecc): the agent was stopped, the delete Run ended "succeeded, 1 warning" and parked the agent (right). When the agent came back it ran the unbind that was still queued from the timed-out Run, cleaned the guest, sent the result and stopped. But the park stayed: the controller dropped the result of the old command id; only the result of a park-retry unbind ends a park. With the agent gone, only "forget" can end it now.
F11 (minor): `/guests/unmanaged` shows `adoptable: true` while the guest is parked (adopt gives 409 `agent-left`).

## Context (read only these)
- `docs/tasks/T-192.log.md` F12, F11 (ask pl-backend for the lines, short) · D-396, PR #309 (`internal/link` park handling, `internal/store/unbind_pending*`)

## Acceptance criteria
- [ ] The result of ANY unbind command that the controller sent to this agent ends the park (the first one from the Run and every retry): credential revoked, row dropped, event `agent.unbound`. A result for a command the controller never sent, or from another agent id, ends nothing (test; this must not become a way for a guest to unpark another guest).
- [ ] No double unbind: when the first command is still queued at park time, the retry does not send a second one before the first is answered or expired (say what you chose).
- [ ] F11: `adoptable` is false with the reason `agent-left` while parked.
- [ ] Tests for the host sequence (stop agent → delete → park → agent back → queued unbind runs → park ends). `make lint test`; PR to `main`; report. Proven on the host in T-192.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-205 – The `unbind` command never leaves the controller (host finding F8)

- **Owner:** pl-architect (author of T-193)   · **Reviewer:** pl-reviewer (repair line = repair path)   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
Found on the real host (T-192): deleting an adopted Target "succeeded", the guests stayed (right), but the agent got no `unbind`: `link.Send()` validates the command type and its switch has no case for `wire.CommandUnbind`. So the guests keep the agent (unit, binary, `/etc/plumb-agent`) **and the `plumb-repair` root line of the controller key**, although Plumb no longer manages them. The engine tests use a fake command path, so CI missed it.

## Acceptance criteria
- [ ] `link.Send` accepts `unbind`. A test sends every command type of `internal/wire` through the **real** link (controller side to a real agent loop), so a new type without a case fails; list which engine tests used the fake path for a command that was never sent for real and move at least the adoption delete to the real path.
- [ ] A Run whose unbind could not be delivered or was not confirmed must not say "succeeded" without a mark: the step is a warning state that the UI and the API show ("the guest still has the agent and the repair line"), with the manual commands, and the Target view of an unmanaged guest lists it as "agent left behind" until a later contact confirms. Decide the exact form (number from `pl-lead` if a decision is needed).
- [ ] The repair key side: after an unbind that was not confirmed, the controller never uses the repair key for that guest again (no Target), and a key rotation counts such guests as "may still hold the old line" in the rotate view (ties in with F7 of T-192: `previous.targets` must not list deleted Targets as normal ones; fix that listing here).
- [ ] CI guest test (VM job): adopt → delete → the agent unit, binary, `/etc/plumb-agent` and the repair line are gone; a person's own `authorized_keys` lines stay.
- [ ] `make lint test`; PR to `main`; report. Proven again on the host in T-192.

## Result (owner, max 8 lines)
branch · commit · tests · notes

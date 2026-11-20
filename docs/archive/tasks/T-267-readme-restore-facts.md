# T-267 – README: what a user must know after a restore and after an upgrade (facts of T-263)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
The host re-run (T-263, R1) showed four things a user meets and the README does not say, or not fully. User docs only: no decision or task numbers, no host data.

## Acceptance criteria
- [ ] G1: after a restore of Plumb the password that the installer shows once works for one login and stops working after the forced change; old passwords fail.
- [ ] G2: the restore confirm: the UI path stays; add the API form (`POST /restore/confirm` with `old_controller_off: true`, as the openapi says) and why Plumb asks for it (two controllers must never manage the same guests).
- [ ] G3: a restore locks every other user and disables the API tokens until a person enables them again; how to unlock a user (the one-time password) and a token.
- [ ] G4: agents never update by themselves: after a controller upgrade each Target shows "update available" and needs one agent-update Plan; after a GUEST restore the agent runs the version of the backup and needs the same.
- [ ] Each sentence is checked against the code or the T-263 log (say where, in the PR's evidence comment). `make lint`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

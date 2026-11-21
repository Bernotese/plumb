# T-164 – `network`: low notes of the review of PR #249

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-150 merged

## Goal
Close the low notes that the review of PR #249 (T-150) left open, and re-check the late fix.

## Context (read only these)
- PR #249: review comment of pl-reviewer (n1, n3, n4 and point 4) and the last two commits (route wait)

## Acceptance criteria
- [ ] n3: the default gateway of the controller is in the reserved address list (refused as an extra address), fail closed.
- [ ] n4: when arming the rollback fails, the confirmed copy is removed (nothing half-armed stays).
- [ ] Point 4: a Plan that changes `network` on a DHCP Target shows a display-only note that the server cannot check the main address (D-356).
- [ ] n1 (a reboot inside the window while the agent does not start keeps the new file): say in the PR whether a boot-time restore like `plumb-agent-firewall` is small and safe for `network`. If yes, build it with a guest test; if not, write the limit into `docs/ARCHITECTURE.md` §17 and tell `pl-lead` for the README (T-159).
- [ ] The route wait of 121c9ba (30 s bound) is reviewed here by pl-reviewer: the apply cannot hang the agent's other work for that time, and the safe-apply window is not started before the wait.
- [ ] Tests: `make lint test` passes; guest test in CI. PR to `main`, report.

## Out of scope
- More NICs, VLAN (slice 7).

## Result (owner, max 8 lines)
branch · commit · tests · notes

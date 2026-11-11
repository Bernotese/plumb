# T-067 – The agent reports its addresses; Issue when a guest does not use its configured address

- **Owner:** pl-architect (plan first), then tbd   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** –   · **State:** planning (user go for the plan: 2026-11-11); it changes the agent, so the user approves the plan before any code

## Goal
Plumb shows the addresses a guest really uses, and opens an Issue when a Target with a static address does not use it.

## Situation
- T-015 on the real host: a stock Ubuntu 26.04 guest stayed on DHCP although the Target had a static address (D-192). The agent enrolled, and Plumb showed the configured address (D-079). Nobody saw the fault until SSH failed.
- Proposal of pl-architect (D-192 discussion, point 5): Hello gets `addrs` (global unicast addresses of NICs that are up; bounded and validated like the other Hello fields). The controller stores them; `ips` shows the reported addresses when the agent is online. Static Target and configured address not in the list: Issue on `guest:config`, attribute `ip`. `ip` is create-only (D-185), so the Issue offers no revert and no accept, only ignore, and the text says "the guest does not use the configured address". Reported addresses are for display only: never for authorization, never as a connect target (root on the guest can lie).

## Context (read only these)
- `docs/DECISIONS.md` D-079, D-185, D-192, D-169 (no agent version gate yet), D-091 (ignore)
- `internal/agent` (Hello), `internal/link`, `internal/wire`, `internal/issue`, `api/openapi.yaml` (Target status `ips`)
- `docs/tasks/T-015.log.md` (the Ubuntu finding)

## Acceptance criteria (draft; the plan refines them)
- [ ] The agent sends its addresses with Hello and when they change. Bounds and validation are defined.
- [ ] The API shows reported addresses, marked as reported by the guest.
- [ ] A static Target whose address is not reported gets an Issue with a clear text; it closes by itself when the address appears. No revert, no accept.
- [ ] An older agent that sends no `addrs` gives no Issue (D-169).
- [ ] Reported addresses are never used for authorization or as a connect target. Test.
- [ ] Guest test in CI; one check on the real host with the stock Ubuntu 26.04 image (static address, first boot: Issue; after the first restart: resolved).

## Out of scope
- A fix for Ubuntu 26.04 itself (T-039).

## Plan (architect)

## Result (owner, max 8 lines)
branch · commit · tests · notes

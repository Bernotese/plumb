# T-019 – Record the user's answers to ARCHITECTURE §11

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer (part of the PR #1 review)   · **QA:** –
- **Size:** S   · **Depends on:** T-016

## Goal
The pending decisions are final, so PR #1 can be approved and merged.

## User answers (2026-11-10)
1. **Test host:** comes later. The user deploys one on 2026-11-11 at the earliest. T-012 and T-013 (and any other real-host work) stay blocked until `pl-lead` confirms the host. All other cards must not depend on it.
2. **Proxmox version:** PVE 9.1 or newer only. Accepted.
3. **Exposure:** internal application only (LAN). Certificate from the Plumb CA. Accepted.
4. **Master key:** set up once at initial setup. `pl-lead` reads this as the recommended option: key file inside the Plumb LXC (volume without backup), recovery key shown once at install, no passphrase at start.
5. **UI stack:** Svelte 5 (recommendation accepted).

## Acceptance criteria
- [ ] D-019, D-030, D-032 are no longer "pending" in DECISIONS.md. Q1 and Q3 are logged too if a decision depends on them.
- [ ] ARCHITECTURE §11 shows the answers, or is reduced to what is still open (only the test host date).
- [ ] The wave plan shows which cards can start without the test host.
- [ ] Same branch and PR (#1), one small commit, no force-push.

## Result (owner, max 8 lines)
branch · commit · tests · notes

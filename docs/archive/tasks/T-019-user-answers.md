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

## Part 2 – shoulds from the second review (verdict: approve)
Review: https://github.com/Bernotese/plumb/pull/1#pullrequestreview-5835523725 (0 must, 3 should, 4 nit)
- [ ] Should 1: after cloud-init is disabled, cloud-init field changes (ip, sshkeys) and disk grow have no effect in the guest. Make them create-only in Driver.Diff, or add a guest resize.
- [ ] Should 2: a plain hash of a sensitive value can be brute-forced. Report changed true/false, or use an HMAC (D-037, T-005).
- [ ] Should 3: T-018: on 401 the agent re-enrolls from the token source (D-038).
- [ ] Nits: fix the cheap ones.

## Result (owner, max 8 lines)
T-019 done (part 1 + 2) · branch `pl-architect/T-001-architecture` · PR #1 · tests: none (docs only)
- Part 1: D-019, D-030, D-032 final. New D-040 (LAN only, Plumb CA certificate), D-041 (test host later). §11 shows the answers; wave plan lists cards without the host.
- Should 1: `ip`, bootstrap user and `disk` are create-only in the slice; disk grow moved to the roadmap (D-025, §5, T-009, T-010, T-012, T-006).
- Should 2: sensitive attributes report only changed / not changed, no hash (D-037, §7, T-003, T-005).
- Should 3: the agent re-enrolls on 401 (D-038, §5, T-018).
- Nits: all 4 fixed (smbios `uuid` kept, CA key backup note, decision range, Makefile stubs in T-003/T-017).

# T-181 – Agent: live drift of ip / nft changes

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** – (hello field after T-173)

## Goal
Slice 6 part A, child card **A7** of T-172. A hand change with `ip` or `nft` in a guest is reported within seconds, not at the 15 min full check (BRIEF §5.8, D-367).

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions `docs/decisions/T-172.md` on the same branch (D-360 … D-375). Read row A7 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A7 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Netlink is a trigger only: the report still comes from the normal Observe. Fuzz test for every netlink parser/header check (a short or unaligned message must never panic the agent – see the memory note on `syscall.ParseNetlinkRouteAttr`). Debounce so a flapping link cannot flood (the D-366 limit comes with T-180).
- [ ] CI guest test in the VM job: address del, route add, `nft add rule` → report within 10 s. Without the capability (LXC, old kernel) the agent says `live_watch: false` and the 15 min check stays.
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-181.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures in a PR only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

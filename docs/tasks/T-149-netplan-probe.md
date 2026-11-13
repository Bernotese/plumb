# T-149 – Netplan probe in the CI guests

- **Owner:** pl-codex-qa   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** S (Codex)   · **Depends on:** –

## Goal
Slice 5, child card **C5** of T-138. Show on the real Debian 13 and Ubuntu 26.04 cloud images what the `network` resource can rely on.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan" (until PR #241 is merged: `git show origin/pl-architect/T-138-road-to-release:docs/tasks/T-138-road-to-release-slice-5-plan.md`), decisions `docs/decisions/T-138.md` on the same branch. Read row C5 of the child card table, the contracts, the risks.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C5 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] `docs/plans/netplan-probe.md`: netplan present yes/no, renderer, file match of `50-cloud-init.yaml`, and whether `netplan apply` drops the agent TCP link (measured, per image). Facts from the CI run, not from memory.
- [ ] Own worktree and branch. The probe test must not make the normal guest job slower by more than ~1 min.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-149.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any new token right. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

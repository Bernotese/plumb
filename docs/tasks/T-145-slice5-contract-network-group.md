# T-145 – Slice 5 contract: network, group, generic safe apply

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-157)
- **Size:** S   · **Depends on:** T-149, T-160 (facts), T-144 merged

## Goal
Slice 5, child card **C1b** of T-138. Fix the schemas of `network` and `group` and the generic safe apply so that T-150 and T-151 can start.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan", decisions D-327 … D-347 (`docs/DECISIONS.md`; until merged in `docs/decisions/T-138.md`, `T-144.md`), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §17. Read row C1b of the child card table, the contracts, the risks, and the review comments of PR #241 / #243 that name this row.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C1b of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Uses the measured facts of T-149 (`docs/plans/netplan-probe.md`) and T-160 (`docs/plans/debian-network-facts.md`): Debian backend (netplan or networkd), Ubuntu merge with `50-cloud-init.yaml`. If the facts make `network` unsafe on one OS, stop and tell `pl-lead` before T-150 starts.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-145.md`.
- [ ] No new token right. Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-150 – Generic safe apply and resource network

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-157)
- **Size:** L   · **Depends on:** T-145 merged

## Goal
Slice 5, child card **C6** of T-138. The `network` resource (VM: extra addresses, routes, name servers, search) on a safe apply that `firewall` shares.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan", decisions D-327 … D-347 (`docs/DECISIONS.md`; until merged in `docs/decisions/T-138.md`, `T-144.md`), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §17. Read row C6 of the child card table, the contracts, the risks, and the review comments of PR #241 / #243 that name this row.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] **Prove first (review G3 of #244), before the safe-apply work:** a CI guest test on the Debian 13 vendor cloud image (netplan 1.1.2) and on Ubuntu 26.04 shows that a second file `/etc/netplan/60-plumb-<name>.yaml` under the same key appends to `addresses`, `routes` and `nameservers.addresses` of `50-cloud-init.yaml` (`netplan get` before and after). If a list is replaced instead, stop and tell `pl-lead` (the contract of T-145 must change).
- [ ] Server refusals of T-145 G1 (controller, node, every Target's main address and gateway, another Target's extra address) and the agent duplicate-address probe, each with a test.
- [ ] Everything row C6 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] `firewall` moves onto the generic code with its slice 4 tests unchanged, incl. the CI guest rollback test.
- [ ] CI guest tests for `network` incl. a forced rollback; the schema refuses the main address and a default route; `network` on an LXC is refused at save.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-150.md`.
- [ ] No new token right. Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

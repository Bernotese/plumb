# T-116 – Agent: safe apply and resource `firewall`

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** T-111   · **Parent:** T-107 (C6)

## Goal
The agent applies firewall rules and puts the old rules back by itself when it loses the controller.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C6, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-116.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] Safe apply (D-293): keep the old state, apply, need a fresh controller round trip within 90 s, else restore and report `failed` / `rolled-back`; the guard of D-271 covers an agent crash
- [ ] Resource `firewall` (D-295): one table `inet plumb`, atomic `nft -f`, input only, loopback and `established,related` always kept; `drop` without an SSH accept refused unless `allow_no_ssh: true` (save, schema and agent)
- [ ] CI guest tests: Debian 13 and Ubuntu, VM and unprivileged LXC; a forced rollback; nftables in an LXC
- [ ] `make lint test build` and `make test-guest` green

## Out of scope
- `network`; the Proxmox firewall
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

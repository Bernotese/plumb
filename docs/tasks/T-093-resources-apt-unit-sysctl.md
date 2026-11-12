# T-093 – Agent resources apt_repository, unit, sysctl

- **Owner:** pl-reviewer (as implementer)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-090   · **Parent:** T-089 (C4)

## Goal
Three new resource types with drift detection.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-093.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [ ] `apt_repository`: deb822 file, `Signed-By` names its own inline key file, never `trusted.gpg.d`, `Trusted: yes` refused; the key is inline text, never downloaded (D-260)
- [ ] `unit`: unit file + daemon-reload; timers are units
- [ ] `sysctl`: declares `vm` only; refused for an LXC Target
- [ ] Each has check, apply, drift and a guest test on Debian 13 and Ubuntu 26.04
- [ ] `make lint test build` and `make test-guest` pass

## Out of scope
- `command` (T-094)
- `network`, `firewall` (slice 4)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

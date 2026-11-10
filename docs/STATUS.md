# Status

Product: **Plumb** – self-hosted infrastructure controller for a Proxmox homelab. Brief: `docs/BRIEF.md`.
(Naming: the product is "Plumb". The herdr workspace and local folder are still called `plump`.)

## Phase
First slice: one VM, from an image, configured by the agent, drift as an issue. Architecture approved by the user on 2026-11-10 (PR #1).
Wave plan: `docs/tasks/T-001-architecture.md`, Plan section.

## Tasks
| Card | Owner | State |
|---|---|---|
| T-003 Go scaffold + contracts | pl-architect | PR #3 open, QA + review running |
| T-017 OpenAPI + web skeleton | pl-frontend | PR #2 reviewed (approve); merge after PR #3, then Makefile lines + QA |
| T-004 state/store → T-009 link/events/fake driver | pl-backend | waits for T-003 |
| T-005 resources → T-018 agent loop | pl-frontend | waits for T-003, T-017 |
| T-007 guest test harness | pl-codex-qa | waits for T-003 |
| T-008 starter blocks | pl-codex-fix | waits for T-003 |
| T-006, T-010, T-011, T-014, T-015 | see cards | later waves |
| T-013 installer → T-012 Proxmox driver → real-host part of T-015 | pl-backend | **blocked: test host (D-041)** |

## Done
- 2026-11-10 · Team setup, auto mode, GitHub remote, model split.
- 2026-11-10 · Brief stored, decisions D-001 … D-017 seeded.
- 2026-11-10 · T-001, T-002, T-016, T-019: architecture, review (approve), decisions D-018 … D-041, slice cards. Merged as a9b4579.

## Open for the user
- Provide the Proxmox test host (PVE ≥ 9.1; needs: ARCHITECTURE §8). Planned from 2026-11-11.

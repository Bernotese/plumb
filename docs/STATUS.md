# Status

Product: **Plumb** – self-hosted infrastructure controller for a Proxmox homelab. Brief: `docs/BRIEF.md`.
(Naming: the product is "Plumb". The herdr workspace and local folder are still called `plump`.)

## Phase
First slice: one VM, from an image, configured by the agent, drift as an issue. Architecture approved by the user on 2026-11-10.
Wave plan: `docs/tasks/T-001-architecture.md`, Plan section. Secret store is not part of the slice.

## Tasks
| Card | Owner | State |
|---|---|---|
| T-017 OpenAPI + web skeleton | pl-frontend | PR #2: aligning with T-003 contracts (13 points), then QA |
| T-004 state/store → T-009 link/events/fake driver | pl-backend | T-004 in progress (wave 1) |
| T-007 guest test harness | pl-codex-qa | PR #5 in review; guest run **blocked: Docker access for jannes (user action)** |
| T-008 starter blocks | pl-codex-fix | PR #6: QA + review running |
| T-005 resources → T-018 agent loop | pl-frontend | starts after T-017 |
| T-006, T-010, T-011, T-014, T-015 | see cards | later waves |
| T-013 installer → T-012 Proxmox driver → real-host part of T-015 | pl-backend | **blocked: test host (D-041)** |

## Done
- 2026-11-10 · Team setup, auto mode, GitHub remote, model split.
- 2026-11-10 · T-001, T-002, T-016, T-019: architecture, review, decisions D-001 … D-041, slice cards (a9b4579).
- 2026-11-10 · T-003: Go scaffold, contracts, migration 0001, Makefile, CI, decisions D-042 … D-049 (41eb5ef).

## Open for the user
- Give `jannes` Docker access on the dev VM (blocks the T-007 guest run).
- Provide the Proxmox test host (PVE ≥ 9.1; needs: ARCHITECTURE §8). Planned from 2026-11-11.

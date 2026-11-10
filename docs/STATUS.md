# Status

Product: **Plumb** – self-hosted infrastructure controller for a Proxmox homelab. Brief: `docs/BRIEF.md`.
(Naming: the product is "Plumb". The herdr workspace and local folder are still called `plump`.)

## Phase
First slice: one VM, from an image, configured by the agent, drift as an issue. Architecture approved by the user on 2026-11-10.
Wave plan: `docs/tasks/T-001-architecture.md`, Plan section. Secret store is not part of the slice.

## Tasks
| Card | Owner | State |
|---|---|---|
| T-012 Proxmox VM driver | pl-backend | draft PR #20: offline part statically approved (f70f8a9). Real-host part (22 items) **blocked: test host** |
| T-021 flow test with the real agent | pl-codex-qa | draft PR #21: done, green; review queued; merges after #8, #13 |
| T-022 agent: 4xx on drift flush | pl-backend | in progress (stacked on T-018 branch) |
| T-007 guest test harness | pl-codex-qa | PR #5: static review approve; merge after the first real run. **Blocked: Docker access (user action)** |
| T-005 resources | pl-frontend | PR #8: static review approve (ea35c1e); merge after the first real guest run. **Blocked: Docker access** |
| T-018 agent loop + drift | pl-frontend | PR #13 (stacked on #8): static review approve (29aa0a4); merge after #8 and the first real guest run. **Blocked: Docker access** |
| T-014 UI on the real API | pl-frontend | in progress |
| T-015 slice acceptance | pl-codex-qa | after T-021 |
| T-020 symlink resource type | tbd | backlog (after the slice) |
| T-013 minimal installer | pl-backend | draft PR #26: offline part done (dry-run, 15 items to verify on host), static review queued. Real-host part **blocked: test host** |

## Done
- 2026-11-10 · Team setup, auto mode, GitHub remote, model split.
- 2026-11-10 · T-001, T-002, T-016, T-019: architecture, review, decisions D-001 … D-041, slice cards (a9b4579).
- 2026-11-10 · T-003: Go scaffold, contracts, migration 0001, Makefile, CI, decisions D-042 … D-049 (41eb5ef).
- 2026-11-10 · T-008: starter Blocks deploy-user, motd, nginx (PR #6).
- 2026-11-10 · T-017: OpenAPI contract, web skeleton, web build in Makefile and CI (PR #2, d4d481b).
- 2026-11-10 · T-004: state package, migration 0002, decisions D-050 … D-053 (PR #7).
- 2026-11-10 · T-009: agent link, event stream, fake driver, race step in CI, decisions D-060 … D-064 (PR #11).
- 2026-11-10 · T-010: plan/approve/apply engine, issues, crash recovery, D-065 … D-069, D-075 (PR #14).
- 2026-11-10 · T-011: HTTP API, sessions, token tiers, SSE, embedded UI + agent binary, serve --dev, D-070 … D-074 (PR #17).
- 2026-11-10 · T-023: dev login + demo data for screenshots, dev only, D-080 (PR #23).
- 2026-11-10 · T-006: web UI slice, 8 screens, mock build separate from production (PR #19).

## Open for the user
- Restart `pl-codex-fix` (Codex session error after `/new`: "permission profile cannot be safely represented by the legacy app-server sandbox policy").
- Give `jannes` Docker access on the dev VM (blocks the T-007 guest run).
- Provide the Proxmox test host (PVE ≥ 9.1; needs: ARCHITECTURE §8). Planned from 2026-11-11.

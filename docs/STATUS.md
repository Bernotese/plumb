# Status

Product: **Plumb** – self-hosted infrastructure controller for a Proxmox homelab. Brief: `docs/BRIEF.md`.
(Naming: the product is "Plumb". The herdr workspace and local folder are still called `plump`.)

## Phase
First slice: one VM, from an image, configured by the agent, drift as an issue. Secret store is not part of the slice.
Controller, API and UI are on `main`. Try it: `make build && bin/plumb serve --dev --dev-demo` (simulated agents, no Proxmox).
**Slice 1 is blocked on two user actions (see below).** Slice 2 part A is running (ARCHITECTURE §12, D-085 … D-101). The user approved the plan on 2026-11-10. Migration merge order: 0003 T-028 → 0004 T-031 → 0005 T-032 (D-096).

## Tasks
| Card | Owner | State |
|---|---|---|
| T-007 guest test harness | pl-codex-qa | PR #5: static approve; merge after the first real run. **Blocked: Docker access** |
| T-005 resources | pl-frontend | PR #8: static approve (fd7c9e6); merge after the first real guest run. **Blocked: Docker access** |
| T-018 agent loop + drift | pl-frontend | PR #13: static approve (5e5f86d); merge after #8. **Blocked: Docker access** |
| T-021 flow test with the real agent | pl-codex-qa | draft PR #21: approved; merges after #8, #13 |
| T-022 agent: 4xx on drift flush | pl-backend | draft PR #27: approved; merges (squash) after #8, #13 |
| T-015 slice acceptance (HTTP) | pl-codex-qa | waits for T-018 on main; real-host part waits for the test host |
| T-012 Proxmox VM driver | pl-backend | draft PR #20: offline part statically approved. Real-host part (22 items) **blocked: test host** |
| T-013 minimal installer | pl-backend | draft PR #26: offline part statically approved (b72da53: 3 minors fixed, delta check with host changes). Real-host part (20 items) **blocked: test host** |
| T-029 secret references → T-033 export/import | pl-backend | T-029 mostly done; its human-only part moves onto T-032 and merges after it |
| T-032 ignore actions (migration 0005, Plan.human_only) | pl-reviewer (implements; pl-architect reviews) | running |
| T-034, T-035 e2e parts (history, rollback, tokens, secrets) | pl-frontend | running, against the real API |
| T-036 UI secret params + ignore | pl-frontend | PR #48 (mock part, base main): review by pl-architect and QA running; e2e waits for T-029, T-032 |
| T-037 slice 2 acceptance, leak hunt | pl-codex-qa | waits for T-029 … T-033 on main |
| T-038 LXC targets, T-039 image builds | tbd | design only. **Blocked: test host** |
| T-020 symlink resource type | tbd | backlog (after the slice) |
| T-040 bad-bearer limit scope | tbd | backlog |

Merge order when Docker works: #5 (real run, digest pins) → #8 → #13 → #21, #27 → T-015.
Order with the test host: T-013 (dry-run first) → T-012 → wire `serve` to `PLUMB_PVE_*` → real-host part of T-015.

## Done (all 2026-11-10)
- Team setup, auto mode, GitHub remote, model split.
- T-001, T-002, T-016, T-019: architecture, review, decisions D-001 … D-041, slice cards.
- T-003: Go scaffold, contracts, migration 0001, Makefile, CI (PR #3).
- T-008: starter Blocks deploy-user, motd, nginx (PR #6).
- T-017: OpenAPI contract, web skeleton (PR #2).
- T-004: state package, migration 0002 (PR #7).
- T-009: agent link, event stream, fake driver, race step in CI (PR #11).
- T-010: plan/approve/apply engine, issues, crash recovery, D-075 (PR #14).
- T-011: HTTP API, sessions, token tiers, SSE, embedded UI + agent binary, serve --dev (PR #17).
- T-023: dev login + demo data, dev only (PR #23).
- T-006: web UI slice, 8 screens (PR #19).
- T-024: dev mode simulated agent for new targets (PR #30).
- T-014: UI on the real API, Playwright e2e (PR #28).
- T-025, T-027: slice 2 plan (PR #32) and API contract (PR #33), approved by the user (D-101).
- T-030: token API (PR #34).
- T-034 mock part: history UI (PR #35).
- T-028: secret store (PR #37), D-102 … D-109, D-130.
- T-035 mock part: settings UI (PR #45).
- T-031: revision API, rollback through a Plan (PR #39), D-115, D-125 … D-128.
- D-131 Plan.rollback_to in the contract (PR #44); closed schemas (PR #46).
- T-026: QA crash recovery test ported to main (PR #31); stale PR #16 closed.

## Open for the user
- Give `jannes` Docker access on the dev VM: `sudo usermod -aG docker jannes` and `sudo setfacl -m u:jannes:rw /var/run/docker.sock`.
- Provide the Proxmox test host (fully updated PVE 9.2: qemu-server ≥ 9.2.11, libpve-storage-perl ≥ 9.1.13; needs: ARCHITECTURE §8).
- Optional: delete old temp dirs `/tmp/plumb-e2e-*` on the dev VM.

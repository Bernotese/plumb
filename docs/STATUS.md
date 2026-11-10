# Status

Product: **Plumb** – self-hosted infrastructure controller for a Proxmox homelab. Brief: `docs/BRIEF.md`.
(Naming: the product is "Plumb". The herdr workspace and local folder are still called `plump`.)

## Phase
First slice: one VM, from an image, configured by the agent, drift as an issue. Secret store is not part of the slice.
Controller, API and UI are on `main`. Try it: `make build && bin/plumb serve --dev --dev-demo` (simulated agents, no Proxmox).
The user merged PR #8, #5, #13 on 2026-11-10. The real guest tests run in the CI of `main` and are green (Debian 13, Ubuntu 26.04). The whole agent track of slice 1 is on `main` (#5, #8, #13, #21, #27). **Slice 1 is accepted without Proxmox** (T-015, PR #67: HTTP API + real agent). The agent side is proven as root in real Debian 13 and Ubuntu 26.04 guests in CI (resources, the whole agent loop, secret masking in real tool output). Open: everything on the real host. Backlog: T-044 (order between Block uses, then `nginx-site`). **The Proxmox test host `pve-dev` is ready** (address: `~/.config/plump/testhost.env`, not in the repo; rules: AGENTS.md). pl-lead updated it on 2026-11-11 with the user's go: pve-manager 9.2.26, qemu-server 9.2.15, libpve-storage-perl 9.1.15, rebooted, no failed unit. Before the update the installer dry run refused the old packages and changed nothing (T-013 log). **Slice 2 part A is complete on `main`** (ARCHITECTURE §12, D-085 … D-158): secret store and secret parameters, token management, revision history with rollback, ignore actions, export/import, their UI, e2e tests, and the acceptance with leak hunt (zero hits). Part B (image builds, LXC) is designed and waits for the test host.

## Tasks
| Card | Owner | State |
|---|---|---|
| T-015 slice acceptance, real-host part | pl-codex-qa | **test host ready** (the part without Proxmox is merged, PR #67) |
| T-012 Proxmox VM driver | pl-backend | draft PR #20: offline part statically approved. **Real-host part (22 items) running** on the test host. Next card after it: wire `plumb serve` to the driver (`serve` on main stops with "the Proxmox driver is not in this build yet") |
| T-013 minimal installer | pl-backend | draft PR #26. **The installer works on the real host** (run 3, 2026-11-11): roles, ACLs, token, storages, LXC, key volume, `plumb key init`; a half-installed host is repaired. Two defects found by real runs and fixed (token ACL order; umask at `pct create`), plus S1. Passed: rights exact, LXC shape, file modes, no secret in state or journal. Open: negative token checks (running); repair run, UI + first login, agent unit need a `plumb serve` with the Proxmox driver (T-012). Before merge: S2 … S4 of pl-architect, `--remove` test |
| T-038 LXC targets, T-039 image builds | tbd | design only. **Test host ready**; starts after T-012 |
| T-044 order between Block uses (`after`), `restarts`, `nginx-site` | see child cards | plan approved (D-176). **On main**: contract (T-046, #78), web (T-049, #81 + #82), agent (T-048, #83), state + plan (T-047, #80). Running: T-050 Block `nginx-site` (pl-codex-fix). Then T-051 QA with the guest test (pl-codex-qa) |
| T-052 installer: key volume + `plumb key init`, static LXC address | pl-backend | done on the T-013 branch, approved by pl-reviewer (PR #26, 9556b32) |

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
- T-034, T-035 e2e on the real API (PR #51).
- Two flaky tests fixed, both test-side, no product bug (PR #75, #76; D-166).
- T-020: resource type symlink, nginx default site as a link, D-162 … D-165 (PR #73).
- T-043: agent refuses an unsafe state directory at start, D-161 (PR #72).
- T-042: agent loop as root in a real guest; start-up cleanup; apt stderr canary (PR #69, #70).
- Guest harness follow-ups from the run review (PR #66, #68).
- T-015 part without Proxmox: slice 1 acceptance over HTTP with the real agent (PR #67).
- T-021: flow test with the real agent, incl. slice 2 cases (PR #21).
- T-022: agent drift flush survives a permanent 4xx (PR #27).
- Review of the first real guest run (PR #65): harness, resources as root proven; gaps listed.
- T-005 resources (PR #8), T-007 guest harness (PR #5), T-018 agent loop + drift (PR #13): merged by the user; D-054, D-055.
- T-041: all waiting slice 1 PRs in line with main; real agent checked against slice 2; D-159, D-160 (PR #63); agent temp file cleanup (PR #64 into #13).
- T-037: slice 2 acceptance and secret leak hunt (PR #59, #61).
- T-036, T-035 e2e on the real API (PR #60, #62).
- T-033: export/import, encrypted secret bundle, D-150 … D-155, D-158 (PR #56).
- T-029: secret references, D-132 … D-139, D-156 (PR #49).
- T-032: ignore actions, Plan.human_only, migration 0005 (PR #53, local squash), D-145 … D-149.
- T-040: bad-bearer limit scope (PR #55), D-157.
- T-036 mock part: secret parameters, human-only Plans, ignore actions (PR #48).
- T-031: revision API, rollback through a Plan (PR #39), D-115, D-125 … D-128.
- D-131 Plan.rollback_to in the contract (PR #44); closed schemas (PR #46).
- T-026: QA crash recovery test ported to main (PR #31); stale PR #16 closed.

## Dev VM notes
- `jannes` is in the `docker` group since 2026-11-10; local guest runs are possible. Agent sessions that started before that do not have the group yet: run `sg docker -c "make test-guest"` (plain `docker` works after a session restart).

## Open for the user
- Nothing now. (D-180, 2026-11-11: on the test host an agent runs the real installer; the user does not need to.)

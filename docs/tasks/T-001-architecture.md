# T-001 – Architecture and first task cards

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer (T-002)   · **QA:** –
- **Size:** L (Claude)   · **Depends on:** –

## Goal
Plumb has a reviewed architecture that respects D-001 … D-017, and a first set of task cards for the smallest end-to-end slice.

## Context (read only these)
- `docs/BRIEF.md` – product brief. §6 lists the open questions. §2 gives the principles.
- `docs/DECISIONS.md` – binding decisions D-001 … D-017.
- `docs/tasks/_TEMPLATE.md` – card format.
- `AGENTS.md` – team, budget rule, git flow.

## Acceptance criteria
- [ ] `docs/ARCHITECTURE.md` exists, ≤ ~150 lines: overview, components, data model, API contracts, agent protocol, conventions (lint, tests, folders), how to run.
- [ ] It answers every question in BRIEF §6: language + web UI stack, state storage, agent ↔ controller protocol + enrollment + auth, Block/export format and schema language, drift mechanics VM vs. LXC, repo layout.
- [ ] Every answer is logged in `docs/DECISIONS.md` as D-018 … (one line each, with the reason). Follow the brief's recommendations (Go, SQLite) unless you have a strong reason; state the reason.
- [ ] A short security section: trust boundaries, agent enrollment, secret store, Proxmox token scope, API token tiers.
- [ ] A test strategy section: how the team tests without a real Proxmox host (fake driver / recorded API), and what needs a real host. List what the user must provide for real-host tests.
- [ ] Task cards `docs/tasks/T-003 …` for the first slice: one Debian/Ubuntu VM, created from an image, configured by the agent with a few resources, drift reported as an issue. Each card: owner per AGENTS.md budget rule, size, dependencies, testable acceptance criteria. Mark which cards can run in parallel. Keep cards status "proposed"; nobody starts them yet.
- [ ] Open questions that need the user are listed at the end of ARCHITECTURE.md (max 5, each with your recommendation).

## Out of scope
- Any feature code or scaffolding. (Brief §8.3: no code before review + user approval.)
- Detailed design of later features (image rebuild schedule, Compose block, MCP, backups). Name them in a roadmap list only.

## Plan (architect, optional)
First slice = cards T-003 … T-015, all "proposed". Cards in one wave can run in parallel.

| Wave | Cards | Needs |
|---|---|---|
| 0 | T-003 scaffold + contracts | T-002 verdict + user approval |
| 1 | T-004 state/store · T-005 agent/resources · T-006 UI (mock API) · T-007 guest harness · T-008 starter blocks · T-009 link/events/fake driver · T-012 Proxmox VM driver | T-003 only |
| 2 | T-010 plan/apply/issues · T-013 minimal installer | T-004 + T-005 + T-009 · T-012 |
| 3 | T-011 API/auth/events | T-010 |
| 4 | T-014 UI on the real API · T-015 slice acceptance | T-006 + T-011 · T-014 (+ T-012, T-013 for the real host) |

- Backend is the bottleneck: T-004, T-009, T-010, T-011, T-012, T-013 are backend cards. T-005 goes to `pl-reviewer` as fallback implementer; `pl-architect` reviews it.
- T-012 and T-013 need the Proxmox test host (ARCHITECTURE §8, question 1). All other cards need no Proxmox.
- Facts to verify on the real host (in T-012/T-013): `cicustom` and LXC `env` work with the restricted token; `hookscript` stays root-only; `download-url` works with `Sys.AccessNetwork`.

## Result (owner, max 8 lines)
T-001 done · branch `pl-architect/T-001-architecture` · tests: none (docs only; no code allowed)
- `docs/ARCHITECTURE.md` (§1–11), decisions D-018 … D-035, cards T-003 … T-015 (proposed).
- Proxmox facts checked against the public PVE 9.2 API schema, not against a real host.
- Five questions for the user are in ARCHITECTURE §11. Question 1 (test host) blocks T-012/T-013.

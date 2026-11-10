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
First slice = cards T-003 … T-015, T-017, T-018, all "proposed". (T-016 is the rework card.) One row = one agent works top to bottom.

| Agent | Wave 0 | Wave 1 | Wave 2 | Wave 3 |
|---|---|---|---|---|
| pl-architect | T-003 Go scaffold + contracts | review of T-017 contract | – | – |
| pl-frontend | T-017 OpenAPI + web skeleton | T-005 resources → T-018 agent loop | T-006 UI (mock API) | T-014 UI on the real API |
| pl-backend | – | T-004 state/store → T-009 link/events/fake driver | T-010 plan/apply/issues → T-011 API/auth | host track |
| pl-codex-qa | – | T-007 guest harness | tests for merged cards | T-015 slice acceptance |
| pl-codex-fix | – | T-008 starter blocks | review findings | review findings |
| pl-reviewer | – | reviews only | reviews only | reviews only |

- Critical path: T-003 → (T-004 → T-009) and (T-005 → T-018) → T-010 → T-011 → T-014 / T-015. Both wave-1 chains have about the same length.
- `pl-frontend` writes the guest side (T-005, T-018) because the UI is not on the critical path before T-011. `pl-backend` writes the controller side. Both code against the T-003 contracts.
- Host track (not on the critical path, needs the Proxmox test host): T-013 installer → T-012 Proxmox driver → real-host part of T-015. Owner `pl-backend` after T-011. If the host exists earlier, `pl-lead` can give T-013 (M) to a free Claude agent.
- Facts to verify on the real host (T-013, T-012): `cicustom vendor=` and `smbios1 serial=` work with the restricted token; the token cannot write snippets; `hookscript` stays root-only; `download-url` works with `Sys.AccessNetwork`; `import-from` works from the image storage; reboot after enrollment works; LXC `env` works with the token (later, LXC card).

## Result (owner, max 8 lines)
T-001 done · branch `pl-architect/T-001-architecture` · tests: none (docs only; no code allowed)
- `docs/ARCHITECTURE.md` (§1–11), decisions D-018 … D-039, cards T-003 … T-015, T-017, T-018 (proposed). Reworked in T-016.
- Proxmox facts checked against the public PVE 9.2 API schema, not against a real host.
- Five questions for the user are in ARCHITECTURE §11. Question 1 (test host) blocks T-012/T-013.

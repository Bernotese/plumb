# T-025 – Plan slice 2

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M (plan only, no feature code)   · **Depends on:** –

## Goal
The team has a reviewed plan for the work after the first slice. The user can approve it in one read.

## Situation
- Controller, API and UI of slice 1 are on `main`. See `docs/STATUS.md`.
- Two things block the team: Docker access on the dev VM (agent-side PRs) and the Proxmox test host (driver, installer).
- The user said "continue". The team must have useful work that it can finish and verify **now**.

## Context (read only these)
- `docs/BRIEF.md` §5 – the full feature list
- `docs/ARCHITECTURE.md` §10 (roadmap names), §7 (security, D-030 master key), §11
- `docs/DECISIONS.md` – use numbers **D-085 … D-099** for new decisions
- `docs/STATUS.md` – what is merged, what is blocked
- `api/openapi.yaml` – only the parts that you extend

## Acceptance criteria
- [ ] `docs/ARCHITECTURE.md` has a short section "Slice 2" (aim ≤ 60 lines). It names the scope, the order, and the reason for the order.
- [ ] Order rule: first the items that the team can build **and verify** without Docker and without the Proxmox host. Candidates: secret store (store + API + UI, D-030), API token management (API + UI), "Ignore once / Ignore by rule" for issues, export/import of the state, revision history with diff and rollback (check what slice 1 already has).
- [ ] Host-dependent items (LXC targets, image builds with seal/retention/schedule) get a design at decision level only. Their cards are marked `blocked: test host`.
- [ ] Every new decision is in `docs/DECISIONS.md` (D-085 …) with a one-line reason.
- [ ] Secret store design covers: key hierarchy from the master key (D-030), encryption at rest, how a Block parameter refers to a secret, how the value reaches the agent, and how the existing redaction (D-062, `Config.Sensitive`) gets its list. Secrets never appear in plans, logs, events, or exports in clear text.
- [ ] Task cards `docs/tasks/T-027…` for the unblocked items. Each card: owner, size, depends-on, files, acceptance criteria, tests. Cards for backend and frontend can run in parallel (contract first in `api/openapi.yaml`).
- [ ] Each card says if it needs a new architecture decision or stays inside approved decisions.
- [ ] A list "Questions for the user" (max 5, each with your recommendation). Leave it empty if there are none.
- [ ] No feature code. `make lint` passes if you touch `api/openapi.yaml`.

## Out of scope
- Docker Compose block, Block editor, MCP, AI help, agent self-update, repair fallback. Name them as "later" only.
- Changes to slice 1 PRs that wait for Docker or the host.

## Result (owner, max 8 lines)
branch · commit · tests · notes

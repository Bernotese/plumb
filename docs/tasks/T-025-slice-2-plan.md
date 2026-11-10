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
- [x] `docs/ARCHITECTURE.md` has a short section "Slice 2" (aim ≤ 60 lines). It names the scope, the order, and the reason for the order.
- [x] Order rule: first the items that the team can build **and verify** without Docker and without the Proxmox host. Candidates: secret store (store + API + UI, D-030), API token management (API + UI), "Ignore once / Ignore by rule" for issues, export/import of the state, revision history with diff and rollback (check what slice 1 already has).
- [x] Host-dependent items (LXC targets, image builds with seal/retention/schedule) get a design at decision level only. Their cards are marked `blocked: test host`.
- [x] Every new decision is in `docs/DECISIONS.md` (D-085 …) with a one-line reason.
- [x] Secret store design covers: key hierarchy from the master key (D-030), encryption at rest, how a Block parameter refers to a secret, how the value reaches the agent, and how the existing redaction (D-062, `Config.Sensitive`) gets its list. Secrets never appear in plans, logs, events, or exports in clear text.
- [x] Task cards `docs/tasks/T-027…` for the unblocked items. Each card: owner, size, depends-on, files, acceptance criteria, tests. Cards for backend and frontend can run in parallel (contract first in `api/openapi.yaml`).
- [x] Each card says if it needs a new architecture decision or stays inside approved decisions.
- [x] A list "Questions for the user" (max 5, each with your recommendation). Leave it empty if there are none.
- [x] No feature code. `make lint` passes if you touch `api/openapi.yaml`.

## Out of scope
- Docker Compose block, Block editor, MCP, AI help, agent self-update, repair fallback. Name them as "later" only.
- Changes to slice 1 PRs that wait for Docker or the host.

## Plan (pl-architect)
- Design: `docs/ARCHITECTURE.md` §12 (50 lines). Decisions: D-085 … D-098.
- What slice 1 already has: token tiers and `CreateToken` (no route), `store.Revisions` / `Diff` (no route), redaction (`wire.Redact`, `link`) without a secret source. No rollback, export, `Rule` kind or UI for these.
- Part A cards (unblocked): T-027 contract → T-028, T-029 secrets · T-030 token API · T-031 revision API · T-032 ignore actions · T-033 export/import · T-034, T-035, T-036 UI · T-037 QA leak hunt.
- Part B cards (`blocked: test host`): T-038 LXC targets, T-039 image builds. T-039 comes first.
- Lanes: `pl-backend` T-028 → T-029 → T-032 → T-033. `pl-reviewer` implements T-030 and T-031 (small; `pl-architect` reviews them). `pl-frontend` T-034 → T-035 → T-036 on the mock, then e2e. `pl-lead` can route differently.
- `api/openapi.yaml` is not changed here. T-027 does it, because every response needs examples and the mock must follow; that is more than a plan.

## Questions for the user
1. **Ignore by rule:** a rule hides the Issue. Must it also stop Plumb from overwriting that difference at the next apply? **Recommendation: no.** The Plan still shows the difference, marks it "ignored drift" and applies the desired state. To keep a manual change, use "Accept". Reason: one truth for every apply, and no agent change (the agent PRs wait for Docker).
2. **Scheduled image builds:** may Plumb start a scheduled rebuild without a click? **Recommendation: yes.** A human approves the schedule once with the Image document. A build only makes a new template and never changes a target. Retention then removes old versions that no guest needs.
3. **AI tokens and ignore:** may a `plan` token (for example an AI client) use "Ignore once"? **Recommendation: yes.** It changes no guest and no state, and the Issue records who did it. "Ignore by rule" needs an approved Plan anyway.
4. **Agent in LXC templates:** Plumb repacks the Proxmox base template inside the controller and uploads it (one new Go dependency for zstd). The other way is an installer step as root on the host for every new OS release. **Recommendation: repack in the controller.** Plumb keeps zero root access on the host. The final check needs the test host.

## Result (owner, max 8 lines)
pl-architect/T-025-slice-2-plan · see PR · tests: docs only, `api/openapi.yaml` not touched, so no `make lint` run needed · 13 cards T-027 … T-039, D-085 … D-098, ARCHITECTURE §12, 4 questions for the user.

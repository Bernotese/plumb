# T-107 – Plan for slice 4

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** –

## Goal
Write the plan for slice 4. The user approves it before any code exists ("Ohne Freigabe entsteht kein Code").

## Context (read only these)
- `docs/BRIEF.md` §5 and the open points in §6
- `docs/STATUS.md`
- `docs/tasks/T-089-slice-3-plan.md` – format to copy, the leftovers and the slice 4 proposal at its end (Proxmox day-2, scheduled backups + restore proof, `network`/`firewall` resources, raw-mode offer on Issues)
- `docs/DECISIONS.md` – only what you need (token rights: D-081, D-239, D-240; D-227)
- Leftovers: 2 low `npm audit` findings (`deepmerge`, dev-only), GO-2026-5932, the Ubuntu 26.04 limit, flaky `cmd/plumb TestServeNoAnswer`

## Acceptance criteria
- [ ] This card holds the plan in the T-089 format: scope, gap list against the BRIEF, what is out, child cards (owner, size, dependencies, what runs in parallel), risks, real-host acceptance
- [ ] Security first: say for each part which token rights it needs. A new right is a user question (D-081), never a default
- [ ] At most 6 questions for the user, each with one recommendation and one sentence of reason. Plain words; the user reads a German summary from pl-lead
- [ ] Proposed decisions in `docs/decisions/T-107.md` as `proposed`; ask pl-lead for the numbers (next free: D-287)
- [ ] Budget rule kept: Codex cards are narrow (≤ ~150 lines)
- [ ] PR with the card only; pl-reviewer checks it; no code

## Out of scope
- Any code, schema or contract change

## Plan (architect)

## Result (owner, max 8 lines)
branch · commit · tests · notes

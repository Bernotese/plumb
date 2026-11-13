# T-138 – Road to the release and plan of slice 5

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** L (plan only, no code)   · **Depends on:** –

## Goal
The user decided (D-326): no release until every open point of `docs/BRIEF.md` §5/§6 is done, and the team plans and builds slice after slice on its own. We need (1) one list of everything that is still open and its order in slices, (2) the full plan of slice 5.

## Context (read only these)
- `docs/DECISIONS.md` D-326 and `AGENTS.md` "Autonomy and release"
- `docs/BRIEF.md` §5, §6 · `docs/STATUS.md` (rows T-107, "Open for the user") · `docs/ARCHITECTURE.md`
- `docs/tasks/T-107-*.md` as the model for a slice plan
- Prep cards run in parallel and write to `docs/plans/` on their own branches: T-139 (host facts), T-140 (risks), T-141 (web gaps), T-142 (code gaps). Use what is there; do not wait for them. `pl-lead` tells you when one lands.

## Acceptance criteria
- [ ] `docs/plans/ROADMAP.md`: every open point of BRIEF §5/§6 (at least: user management, `network` + more NICs, adoption of existing guests, SSH repair fallback, signed image checksums, MCP / AI help, guest backups, export/import, history rollback, the missing resources and starter Blocks, scheduled image rebuilds — check each against the code, do not trust this list), each with: state today, slice number, size, risks, and a mark **USER** if it needs a new token right or ACL, a public step, or anything outside `pve-dev`.
- [ ] Order: what unblocks most comes first; the USER points are collected early so that `pl-lead` can ask them in one message, long before the slice that needs them.
- [ ] Slice 5 plan in this card ("Plan"): scope, contracts, child cards with owner, size, dependencies and file ownership (cards that can run in parallel must not share files), migration numbers (next: 0017), acceptance on the real host.
- [ ] Plan questions: decide them with your recommendation and write them as decisions (numbers D-327 … D-346) to `docs/decisions/T-138.md`. Only USER points stay open; list them at the top of the card for `pl-lead`.
- [ ] PR to `main` (docs only); `pl-reviewer` checks it; report to `pl-lead`.

## Out of scope
- Code. Child cards are written by `pl-lead` from your plan.
- Any release step.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

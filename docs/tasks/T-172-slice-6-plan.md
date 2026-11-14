# T-172 – Plan of slice 6

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** L (plan only, no code)   · **Depends on:** –

## Goal
Slice 5 is built and accepted on the host (T-158); only small PRs are left. By D-326 the team plans the next slice at once. The user has **not** answered U1 … U5 yet (asked 2026-11-13). So slice 6 must be cut so that work can start without those answers.

## Context (read only these)
- `docs/plans/ROADMAP.md` (rows 9–20, §1 questions) · `docs/DECISIONS.md` D-326 … D-359 · `docs/tasks/T-158.log.md`
- `docs/tasks/T-138-road-to-release-slice-5-plan.md` as the model

## Acceptance criteria
- [ ] Update `docs/plans/ROADMAP.md`: slice 5 rows done; new open points from slice 5 added, each with a slice:
  - per-agent rate limit for drift reports (review of #256; proposal there),
  - immediate drift detection for live `network` / `firewall` hand changes (today only at the 15 min full check; BRIEF §5.8 wants "immediately") — netlink / nft monitor, or say why not,
  - `firewall` set to absent after a hand change is free for a token (review of #264, low),
  - `network`: validate the MAC, compare `permaddr` (review of #268, low),
  - F5 of T-158: a way to prove the `system` approval of a newer signed pin end to end (e.g. a fake vendor source in a CI e2e),
  - anything else you find open in the review comments of #241 … #271 (sweep them; list what you dropped and why).
- [ ] Slice 6 plan in this card: **part A** needs no user answer (roadmap rows 9, 10, 13 and the points above; consider pulling row 16 "MCP server" forward if U5 only decides row 17) and starts at once; **part B** (rows 11 SSH repair, 12 adoption) is planned with both answers of U1/U2 in mind and starts only after the user's answer. Never assume an answer to a U question (D-326 (4)).
- [ ] Same form as T-138: contracts, child cards with owner, size, dependencies, files (parallel cards share none; lesson of slice 5: name the shared files `api.go`, `propose.go`, `plan.go`, `serve.go`, `openapi.yaml` marks, registry files and give the order), migration numbers (next: 0020), risks, real-host acceptance.
- [ ] Lessons of slice 5 as rules in the plan: every new resource type gets the opaque/guest-value classification test (D-359); a host step for each human-only rule with a token; CI guest tests only in the VM job when they need the real image.
- [ ] Decisions with your recommendation in `docs/decisions/T-172.md` (ask `pl-lead` for numbers). PR to `main` (docs only), pl-reviewer checks, report.

## Out of scope
- Code. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

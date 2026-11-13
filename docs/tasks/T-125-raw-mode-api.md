# T-125 – Raw mode in the API: `Issue.source`, `Issue.raw`, `TargetView.raw`

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-111 (on main)   · **Parent:** T-107 (gap: the plan had the web part in C8 but no backend card)

## Goal
The controller serves the raw-mode fields of the contract (D-296, D-301), so the web part of T-118 works on the real API.

## Context (read only these)
- `api/openapi.yaml`: `Issue.source`, `Issue.raw`, `TargetView.raw` (marked planned); D-301 in `docs/DECISIONS.md`
- The Accept path that writes `spec.overrides` and the render rules (ARCHITECTURE §3, §6)

## Acceptance criteria
- [ ] `Issue.source` ({block, use}; null for `guest:*` / `agent:*`), `Issue.raw` (source set and `accept` allowed), `TargetView.raw` (`{resource, attrs, source}` per override) as the contract says; the planned marks are removed
- [ ] No new endpoint and no second write path: edit and "Back to the Block" stay normal proposals on `spec.overrides`
- [ ] Secret values never show in `TargetView.raw` (same masking as the Target view today)
- [ ] API tests incl. access tiers; `make lint test build` green; small diff

## Out of scope
- Web (T-118)
- No new token right (D-081)

## Result (owner, max 8 lines)
branch · commit · tests · notes

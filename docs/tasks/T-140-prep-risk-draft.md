# T-140 – Prep: risk draft for the coming topics

- **Owner:** pl-reviewer   · **Reviewer:** pl-architect (uses it in T-138)   · **QA:** –
- **Size:** M (plan file only)   · **Depends on:** –

## Goal
Name the security risks of the coming topics before the plans are written, so that the plans answer them.

## Context (read only these)
- `docs/DECISIONS.md` D-326 · `docs/BRIEF.md` §5.9 · `docs/ARCHITECTURE.md` (auth, tokens, tiers, human-only rules)
- Your own findings of slice 4 (#218, #222, #224, #236) as the pattern of what went wrong

## Acceptance criteria
- [ ] `docs/plans/risks.md` on your branch, max ~150 lines. Per topic: assets, attacker, the 3–6 worst cases, the rule the plan must hold, and how a test can prove it. Topics: user management (roles, second admin, password reset, lockout, last-admin rule, sessions), adoption of existing guests (data from a guest we never built; D-323), SSH repair fallback (key storage, host key trust, what it may run), MCP / AI help (token tiers, prompt injection through guest data, human-only rules), signed image checksums (key pinning, key rotation), guest backups (secrets in vzdump, D-plan H1 of #206).
- [ ] Push the branch, no PR. Report to `pl-lead`. A review request for a real PR goes first; stop this card for it.

## Out of scope
- Code. Decisions (the architect makes them in T-138).

## Result (owner, max 8 lines)
branch · commit · tests · notes

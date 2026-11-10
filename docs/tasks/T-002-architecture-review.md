# T-002 – Architecture review (incl. security)

- **Owner:** pl-reviewer   · **Reviewer:** –   · **QA:** –
- **Size:** M   · **Depends on:** T-001

## Goal
The user gets an independent check of the architecture before any code is written.

## Context (read only these)
- The T-001 PR (link comes with the assignment).
- `docs/BRIEF.md`, `docs/DECISIONS.md`.

## Acceptance criteria
- [ ] Check: every decision D-001 … D-017 is respected; every BRIEF §6 question is answered and logged.
- [ ] Check against BRIEF §2: is anything more complex than needed? Is any dependency not justified?
- [ ] Threat model: agent enrollment and auth, controller ↔ Proxmox token scope, secret store, API token tiers, the "command" resource, the install script.
- [ ] Check the first-slice cards: right size, right owner, testable criteria, correct dependencies.
- [ ] Findings as PR review comments, each marked `must` / `should` / `nit`. No rewrite of the document.
- [ ] Verdict in the report: `approve` or `changes needed`.

## Out of scope
- Editing ARCHITECTURE.md (the architect fixes findings).

## Result (owner, max 8 lines)
branch · commit · tests · notes

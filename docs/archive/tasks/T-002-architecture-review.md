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
Verdict: **changes needed** · review: https://github.com/Bernotese/plumb/pull/1#pullrequestreview-5835441562 (4 must, 15 should, 9 nit) · tests: n/a
- must 1: symlink in the snippet dir lets the controller read host files through cicustom (breaks D-006).
- must 2: snippet deleted while cicustom stays; next VM start fails.
- must 3: no notify/restart mechanism (nginx block needs it).
- must 4: command resource must be risk class destructive; an apply token is root on the guests.
- D-001..017 respected, BRIEF §6 answered. Rework: T-016.

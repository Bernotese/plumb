# T-134 – Two small findings of the slice 4 host run (T-122 F8, F9)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-113, T-116 (on main)

## Goal
Fix F8 and F9 of `docs/tasks/T-122.log.md`.

## Acceptance criteria
- [ ] F8: a `guest:missing` Issue of a Target that a person deleted has no action left and stays open for ever. When the Target is deleted by an applied Plan, its `guest:missing` Issue is resolved with a clear reason ("the Target was deleted"). The foreign mark and the unknown-guest list keep their rules (D-322): a foreign guest stays listed and is never deleted
- [ ] F9: the 422 for `firewall` with `allow_no_ssh` together with `ssh_port` or `ssh_sources` names the attributes and says the rule in plain words, not `attribute : 'not' failed`. Check the other `not` / `dependentSchemas` rules of the resource schemas for the same raw text and fix them the same way
- [ ] Tests for both; `make lint test build` green; small diff; decisions only if a rule changes (D-325, D-326)

## Out of scope
- User management (F3: there is no way to make a second user; a later slice)

## Result (owner, max 8 lines)
branch · commit · tests · notes

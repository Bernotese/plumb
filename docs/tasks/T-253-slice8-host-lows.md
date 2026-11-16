# T-253 – Small fixes from the slice 8 host acceptance (T-247)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-247 (add each low it finds here; one PR at its end)

## Goal
Lows that the host acceptance of slice 8 finds. Text and small behaviour fixes only; anything bigger gets its own card.

## Acceptance criteria
- [ ] F3 (step V): when the free of a removed disk fails with PVE's "still in use", Plumb's step text and the `guest:volume-left` way out say "a snapshot may hold the volume: remove the snapshot first, then `qm/pct set <id> --delete unusedN`; do not `pvesm free` a volume that an unusedN line names". PVE's own text stays fenced/cut as today (no guest text).
- [ ] (more lows of T-247 are added here by `pl-lead`)
- [ ] Tests for each text; `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

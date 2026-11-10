# T-041 – Bring the waiting slice 1 PRs in line with main

- **Owner:** pl-frontend (#8, #13) · pl-backend (#20, #26, #27) · pl-codex-qa (#21)   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S per PR   · **Depends on:** –

## Goal
The slice 1 PRs that wait for Docker or the test host merge cleanly with `main` again and their tests pass. When Docker or the host arrives, the team can start at once.

## Situation
Slice 2 part A is on `main` (secrets, tokens, revisions, ignore, export/import; migrations 0003 … 0005; D-085 … D-158). PR #8, #13, #20, #21, #26, #27 conflict with `main`. PR #5 is clean.

## Order
1. `pl-frontend`: #8 (T-005 resources), then #13 (T-018 agent loop).
2. After #13: `pl-codex-qa` #21 (real-agent flow test), `pl-backend` #27 (T-022).
3. In parallel from the start: `pl-backend` #20 (T-012 driver), #26 (T-013 installer).

## Acceptance criteria
- [ ] `origin/main` is merged into the branch (no rebase, no force-push). `make lint test` passes. CI is green.
- [ ] No functional change beyond what the merge needs. List every non-trivial conflict resolution in the report.
- [ ] #13 and #21: the real agent works with the slice 2 wire changes: secret values in an apply body, masking of agent output (D-139), resources left out of a planning observe (D-156), `Change.ignored` (D-149), `unverified` changes. Report every gap as a finding for `pl-architect`; do not design a fix alone.
- [ ] #26: note where `plumb key init` must be called (D-086); do not implement it here (follow-up on the T-013 card).
- [ ] Decisions, if any: `docs/decisions/T-041.md` (new rule in AGENTS.md).
- [ ] The earlier approvals stay valid only for a pure merge. If code changed, say so; `pl-reviewer` does a delta check.

## Out of scope
- The real Docker guest run and the real host run (still blocked).

## Result (owner, max 8 lines each)
branch · commit · tests · notes

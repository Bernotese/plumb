# T-212 – Open Issues of Targets that were deleted before D-397

- **Owner:** pl-architect   · **Reviewer:** pl-backend   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
Since T-209 (D-397) a Target delete resolves its open Issues as `gone`. Issues of Targets deleted before that stay open for ever (the test host has 5; any older install can have some). Nobody can act on them.

## Acceptance criteria
- [ ] One-time cleanup at start (migration 0025 or a start sweep; say which and why): every open Issue whose Target does not exist is resolved as `gone` by `system` with the comment of D-325/D-397, one event each. Issues of existing Targets are untouched (test with both).
- [ ] The same check for the other things D-397 drops at a delete (`issue_ignore`, `ignored_drift`): rows of Targets that no longer exist go.
- [ ] The copy-rule rows (value hashes, flags) are NOT touched (D-397, review of #314).
- [ ] `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

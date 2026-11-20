# T-022 – Agent: permanent 4xx on the drift flush must not stop polling

- **Owner:** pl-codex-fix (fallback: pl-codex-qa)   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** T-018 (PR #13 merged)
- **Status:** waits for T-018

## Goal
An agent with a too large drift report keeps working.

## Context (read only these)
- pl-reviewer's delta re-review comment on PR #13 (minor follow-up).
- `internal/agent` (drift flush after hello), `internal/link` limits (400 for > 10k items or > 64 KiB output).

## Acceptance criteria
- [ ] A permanent 4xx (not 401/408/429) on the drift flush does not keep the agent from polling. The agent logs it and either drops or splits/truncates the report; say which in the Result.
- [ ] Test: link answers 400 on the drift report → the agent still polls and handles a command.
- [ ] Tests: `make lint test` passes. Diff ≤ ~80 lines.

## Result (owner, max 8 lines)
branch · commit · tests · notes

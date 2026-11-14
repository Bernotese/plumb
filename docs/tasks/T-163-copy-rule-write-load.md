# T-163 – Copy rule: hostile guests must not slow other agents

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex, ≤ ~120 lines)   · **Depends on:** T-148 merged

## Goal
Close the low DoS notes of the last re-check of PR #248 (T-148): the write load of guest-value hashes.

## Context (read only these)
- PR #248, last review comment of pl-reviewer (d1, d2, rate limit) · the guest-value store code of T-148 (`internal/store`, search `guest_value`) and its starvation test

## Acceptance criteria
- [ ] d1: the chunk writes of the copy rule are serialized across Targets (one mutex or one writer), so several guests cannot interleave their paused chunks and keep the write lock busy. Test with 3 reporters: worst wait of `TouchAgent` stays under the limit of the existing test.
- [ ] d2: a Target at the cap is detected by a count before any write; a report for it returns the overflow result without a write tx. Test: zero write txs for such a report.
- [ ] Rate limit: say with file:line whether a per-agent limit for drift reports exists. If not, do **not** build one; write a short proposal (3 lines) in the PR for `pl-lead`.
- [ ] The fail-closed rules of T-148 stay: all its tests pass unchanged. `make lint test` passes. Own worktree and branch, PR to `main`, report.

## Out of scope
- Changes to the copy rule itself (what counts as a match).

## Result (owner, max 8 lines)
branch · commit · tests · notes

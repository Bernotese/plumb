# T-016 – Architecture rework after review

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** T-002

## Goal
PR #1 gets the verdict `approve`, so the user can approve the architecture.

## Context (read only these)
- Review: https://github.com/Bernotese/plumb/pull/1#pullrequestreview-5835441562 (`gh pr view 1 --comments` / `gh api repos/Bernotese/plumb/pulls/1/comments`)
- `docs/tasks/T-002-architecture-review.md` – summary of the 4 `must` findings.

## Acceptance criteria
- [ ] All 4 `must` findings are fixed in ARCHITECTURE.md / DECISIONS.md / the cards. Security fixes include a negative test in the matching card.
- [ ] Every `should` is fixed, or answered on the PR with one sentence why not. Key ones: secrets in observed state and backups; split T-003 and T-005; wave-1 backend overload (use pl-frontend / pl-reviewer capacity); dev `Secure` cookie vs. `browser-shot`.
- [ ] `nit`: fix the cheap ones, skip the rest without comment.
- [ ] New or changed decisions are logged in DECISIONS.md. ARCHITECTURE.md stays ≤ ~170 lines; move detail into the cards.
- [ ] Same branch and PR (#1). No force-push. Cards stay "proposed".
- [ ] Report lists changed card numbers and any new question for the user.

## Out of scope
- Feature code.

## Result (owner, max 8 lines)
branch · commit · tests · notes

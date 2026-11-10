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
T-016 done · branch `pl-architect/T-001-architecture` · PR #1 · tests: none (docs only)
- must 1 + 2: new design. One static, root-owned snippet from the installer; token in the SMBIOS serial; no host mount; agent disables cloud-init (D-025).
- must 3: `restart_on` (D-036). must 4: `command` is `destructive`; `apply` is root-equivalent, stated in §7 (D-031).
- All 15 `should` fixed; three have a PR reply because the fix differs from the proposal. 8 of 9 nits fixed.
- Cards changed: all of T-003 … T-015. New: T-017 (split of T-003), T-018 (split of T-005). Wave plan: T-001 card.
- No new question for the user. Q4 text changed (key volume without backup).

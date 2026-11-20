# T-179 – Per-token rate limit

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-186)
- **Size:** S (Codex)   · **Depends on:** T-173 merged; `api.go` after T-174

## Goal
Slice 6 part A, child card **A6a** of T-172. A token cannot flood proposals, comments or reads (D-365).

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions D-360 … D-376 (`docs/decisions/T-172.md` on that branch), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §18 (on main). Read row A6a of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A6a of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] The limit reads the `x-plumb-rate` marks; a contract test fails for a route without a mark. 429 with Retry-After, no value echo. Human sessions are not limited by this bucket.
- [ ] A card that adds an API operation updates `web/src/lib/roleHints.ts`, the tier test and the contract marks in the same PR. A new resource type or attribute gets the opaque/guest-value classification test (D-359).
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-179.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch `pl-reviewer/T-179-token-rate` (Codex limit: done by pl-reviewer) · tests: `make lint test` → pass
- `internal/api/tokenrate.go`: one bucket per token and kind (proposal 30/min burst 30, comment 30/min burst 30, read 600/min burst 120); 429 `rate-limited` with whole-second `Retry-After`, the handler does not run, nothing of the request is echoed; event `token.rate_limited` (`{kind}`) at most once per token per 10 min; sessions and unmarked operations (events stream, approve, discard, admin) are not limited; memory bounded (idle full buckets pruned).
- `api.go`: two lines in `Handler` (make the limiter, wrap every route). A GET route without a mark that is not on the exempt list stops `Handler` (panic), so a new read cannot skip the limit.
- Tests: kinds = contract marks both ways (planned operations included); mark rules from the tier table (every token GET has read except `/events`, every token operation answering a Plan has proposal); bucket numbers, refill, event gap, prune with a fake clock; through the API: 429 after 30 proposals, no echo, one event, other kinds free, session never limited, read limit after 120. Mutants 5/6 killed (the survivor `p.human()` is equivalent: a session has no token).
- Contract note: the event description (`api/openapi.yaml`, `token.rate_limited` data `{kind: proposal|comment}`) should also name `read`; D-365 gives one event for every limit.

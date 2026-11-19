# T-273 – Context files: current and short (cleanup point 3)

- **Owner:** pl-lead (`AGENTS.md`, `docs/STATUS.md`) + pl-reviewer (`docs/DECISIONS.md`, `docs/ARCHITECTURE.md`)   · **Reviewer:** pl-backend (ARCHITECTURE vs the code)   · **QA:** –
- **Size:** M   · **Depends on:** T-270, T-272

## Goal
User order D-442, point 3. Every agent that starts in this repo reads short files that are true today.

## Acceptance criteria
- [ ] `AGENTS.md` / `CLAUDE.md` (pl-lead): the team as it is (D-438), the flow, the git rules incl. "remove the worktree and the branch right after the merge", the test host rules; no history, no dates of old events.
- [ ] `docs/STATUS.md` (pl-lead): what is open now and where things are (one screen); the done list moves to `CHANGELOG.md` (user-visible) and `docs/archive/STATUS-history.md` (the rest).
- [ ] `docs/DECISIONS.md` (pl-reviewer): no decision is lost. Each superseded decision is marked with the number that replaced it (a table "superseded by" is enough); decisions "accepted and living in docs/decisions/ until merged" are checked (is the feature merged? then say so); the header is true.
- [ ] `docs/ARCHITECTURE.md` (pl-reviewer, checked by pl-backend against the code): no stale "planned"/"later"/"not built yet" text for things that exist; things that do not exist are marked as not built; section numbers that other docs and the code cite stay stable (or every citation is fixed: grep `ARCH §`).
- [ ] The contract marks: `x-plumb-planned` and the planned-fields guard list only what is really not built (the test that checks them stays green).
- [ ] `make lint`; docs PRs by file; report with the line counts before/after.

## Result (owner, max 8 lines)
branch · commit · tests · notes

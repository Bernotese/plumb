# T-256 – Leak hunt, repo part (release slice C5a)

- **Owner:** pl-reviewer   · **Reviewer:** pl-backend (the method); `pl-lead` reads the hit list   · **QA:** –
- **Size:** M   · **Depends on:** –

## Goal
Release slice, card **C5a** of `docs/plans/release-slice.md`. The repo is public. Before a release we know that no secret, address, key or token of the test host or of the dev VM is in anything GitHub serves for this repo.

## Context (read only these)
- `docs/plans/release-slice.md`: section 3 row "Leak scan", section 5 row C5a, section 5a "P4" (the scope, the exact-value files, the scanner, output = places only).
- `AGENTS.md`: "Test host", "Git". Memory: no token ids in logs, no secrets on a command line.

## Acceptance criteria
- [ ] Scope exactly as P4: every ref of `origin` incl. `refs/pull/*` (fetch into a scratch clone, not into a worktree of the team), the text of every PR, issue, review and comment, the Actions logs and artifacts GitHub still keeps, the web bundle built from `main`.
- [ ] Two passes: the pinned scanner with Plumb's patterns, and the **exact-value compare** against the values in the dev VM files under `~/.config/plump/` (addresses, token secrets, keys, passwords, fingerprints, the host's MACs if a file has them). Values are read from the files inside the tool: never on a command line, never in a log, a report or the PR. You may read those files for this card only; do not copy them.
- [ ] Output = places only: ref/commit/path/line or PR/comment id, the kind of the value, never the value. The hit list goes to a 0600 file outside the repo; the PR has the method, the counts and the "zero hits" or "N places" line.
- [ ] **On a hit: stop and report to `pl-lead` at once.** Do not rewrite history, do not delete a comment, a run or a branch, do not force-push (forbidden). `pl-lead` writes the rotation card; a secret in the git history is a question for the user.
- [ ] The tool as a script in the repo (`scripts/` or `test/`), so that C5b can run it again on the RC; no secret value in it. `make lint`; PR to `main`; report.

## Result (owner, max 8 lines)
- `pl-reviewer/T-256-leak-hunt-repo` · tool `scripts/leak-scan.py` · method and counts in `docs/tasks/T-256.log.md`; the place list is a 0600 file outside the repo (given to pl-lead).
- **Secrets: 0** in git (all refs), the 992 GitHub texts, 1 504 Actions run logs (21 never ran) and the web bundle. gitleaks: only made-up fixtures and public examples.
- 4 private example addresses equal dev VM values (history only; the current tree has only the dev VM address that AGENTS.md names). Decision (b) of pl-lead: no rewrite, no rotation, the 10 text places stay; the tree needs no change.
- tests: `make lint test` (see the PR).

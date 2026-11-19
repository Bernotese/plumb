# T-272 – Docs: keep what is needed, archive the rest (cleanup point 2)

- **Owner:** pl-reviewer (after T-270)   · **Reviewer:** pl-lead   · **QA:** –
- **Size:** M   · **Depends on:** T-270

## Goal
User order D-442, point 2. `docs/` holds what a reader or a decision needs. The rest is in `docs/archive/` or gone. No secret in anything that stays.

## Acceptance criteria
- [ ] `docs/tasks/` (338 files): **keep** in place the template, the host logs (`T-158`, `T-192`, `T-224`, `T-225`, `T-244`, `T-247`, `T-263`, `T-266` logs and what else is a real-host or leak-hunt log), and every card or log that `README.md`, `docs/ARCHITECTURE.md`, `docs/DECISIONS.md`, `docs/RELEASE.md`, `CHANGELOG.md`, a plan in `docs/plans/` or the code names by path. **Move** every other card and log to `docs/archive/tasks/` with `git mv` (history stays). Fix the links that the move breaks; a link check script (or `lychee`/a small grep) proves that no doc points to a missing file.
- [ ] `docs/plans/`: plans of finished slices and drafts that are done move to `docs/archive/plans/`; what stays: `ROADMAP.md`, the release plan and what the release still uses, the plan of the slice after the release. `after-release.md` vs `after-release-slice.md`: one file.
- [ ] `docs/screenshots/` (55 files): delete each picture that no doc uses (list them); the rest stays for T-275 (README).
- [ ] Stray files in the repo root (list what is there and why each stays or goes); `.gitignore` covers build output (`./plumb`, `bin/`, `web/dist`, logs).
- [ ] A leak scan of everything that stays AND of everything that moves (`scripts/leak-scan.py --dir docs` with the exact values; places only): zero secrets; identifying values (addresses and the like) listed for `pl-lead`.
- [ ] The loose files in `../plump-wt/` are outside the repo: do not touch them (they are on the list for the user).
- [ ] Numbers for the report: files kept, moved, deleted. `make lint`; one docs PR (or two: the move, then the link fixes); report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

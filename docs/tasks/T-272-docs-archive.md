# T-272 – Docs: keep what is needed, archive the rest (cleanup point 2)

- **Owner:** pl-reviewer (after T-270)   · **Reviewer:** pl-lead   · **QA:** –
- **Size:** M   · **Depends on:** T-270

## Goal
User order D-442, point 2. `docs/` holds what a reader or a decision needs. The rest is in `docs/archive/` or gone. No secret in anything that stays.

## Acceptance criteria
- [x] `docs/tasks/` (338 files): **keep** in place the template, the host logs (`T-158`, `T-192`, `T-224`, `T-225`, `T-244`, `T-247`, `T-263`, `T-266` logs and what else is a real-host or leak-hunt log), and every card or log that `README.md`, `docs/ARCHITECTURE.md`, `docs/DECISIONS.md`, `docs/RELEASE.md`, `CHANGELOG.md`, a plan in `docs/plans/` or the code names by path. **Move** every other card and log to `docs/archive/tasks/` with `git mv` (history stays). Fix the links that the move breaks; a link check script (or `lychee`/a small grep) proves that no doc points to a missing file.
- [x] `docs/plans/`: plans of finished slices and drafts that are done move to `docs/archive/plans/`; what stays: `ROADMAP.md`, the release plan and what the release still uses, the plan of the slice after the release. `after-release.md` vs `after-release-slice.md`: one file.
- [x] `docs/screenshots/` (55 files): delete each picture that no doc uses (list them); the rest stays for T-275 (README).
- [x] Stray files in the repo root (list what is there and why each stays or goes); `.gitignore` covers build output (`./plumb`, `bin/`, `web/dist`, logs).
- [x] A leak scan of everything that stays AND of everything that moves (`scripts/leak-scan.py --dir docs` with the exact values; places only): zero secrets; identifying values (addresses and the like) listed for `pl-lead`.
- [x] The loose files in `../plump-wt/` are outside the repo: do not touch them (they are on the list for the user).
- [x] Numbers for the report: files kept, moved, deleted. `make lint`; one docs PR (or two: the move, then the link fixes); report.

## Result (owner, max 8 lines)
`pl-reviewer/T-272-docs-archive` · docs only, plus one `.gitignore` line (`/plumb-agent`).
- `docs/tasks/`: 344 files → **43 kept**, **301 moved** to `docs/archive/tasks/` (`git mv`). Kept: the template; 27 real-host and leak-hunt logs (T-012, T-013, T-038, T-039, T-053, T-055, T-056, T-061, T-066, T-067, T-073, T-079, T-082, T-083, T-084, T-100, T-106, T-122, T-158, T-192, T-224, T-225, T-244, T-247, T-256, T-263, T-266); the files that a current doc or the code names by path (T-025, T-138, T-172, T-215, T-236 plans, T-195 and T-260 logs); the open cards T-261 (#392), T-263 (step 7) and T-270 … T-275.
- `docs/plans/`: `netplan-probe.md` and `host-facts-slice-8.md` → `docs/archive/plans/`. `after-release.md` merged into `after-release-slice.md` (section "Backlog") and removed. Stay: `ROADMAP.md`, `release-slice.md`, `after-release-slice.md`.
- `docs/screenshots/`: 55 → **36 deleted** (no file names them: T-049, T-081, T-082, T-096, T-097), 19 stay for T-275.
- Repo root: `step2.txt` deleted (an empty file from T-054). The others stay: `.gitignore`, `AGENTS.md`, `CLAUDE.md`, `CHANGELOG.md`, `Makefile`, `README.md`, `VERSION`, `go.mod`, `go.sum`. `.gitignore` already covers `/plumb`, `/bin/`, `dist/`, `*.log`; `/plumb-agent` is added.
- Links: 84 files had their paths rewritten to the new places. A link check over every tracked `.md` (paths and relative links) finds no path broken by this move. What is left are historic or planned names, not links: the removed `docs/decisions/T-NNN.md` files (removed after the append, as AGENTS.md says), the generated `api.gen.ts`, `00NN`/`T-NNN` placeholders, files that old cards removed or that open cards will make.
- Leak scan (`scripts/leak-scan.py --dir docs`, 31 exact values, 386 files): **0 secrets**. One identifying value: the dev VM's browser-shot address (in `docs/archive/tasks/T-014-ui-real-api.md:25`), which is public in `AGENTS.md` already. Not covered by the scan: text in images.

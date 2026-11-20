# T-270 – Check that everything is really finished (cleanup point 0)

- **Owner:** pl-reviewer   · **Reviewer:** pl-lead   · **QA:** –
- **Size:** M   · **Depends on:** –

## Goal
User order D-442, point 0 (`~/workspcae/plump-findings/CLEANUP-BEFORE-RELEASE.md`). Before the release we know, row by row, that every item of the roadmap and of the brief is done, dropped with a decision, or open – with the proof.

## Acceptance criteria
- [x] `docs/plans/ROADMAP.md` row by row, and `docs/BRIEF.md` §5 and §6 item by item: state (done / dropped with its D-number / open) and the proof (the PR number on `main`, and the host log file where a host proof exists: `docs/tasks/T-158.log.md`, `T-192.log.md`, `T-224.log.md`, `T-225.log.md`, `T-247.log.md`, `T-263.log.md`). Check the proof: the code or the test is on `main` today (grep), not only that a PR merged once.
- [x] Fix the ROADMAP table: its "state" column shows the old state for some rows (for example rows 17 and 35 … 39 say "missing", slice 8 built them). One table that is true today.
- [x] A list "really still open": each item with what BRIEF asks, what exists, what is missing, the size, and whether it needs a new token right or ACL (then it is a question for the user: do not plan it). Also list what is only proven by tests and never on the host (for example the one-time-password unlock of other users after a restore), and the known unexplained point O1.
- [x] No code change in this card. Use sub-agents for the wide reading; keep the report small. Output: the ROADMAP fix and `docs/plans/finished-check.md` (the table + the open list) in one docs PR; report the open list to `pl-lead` in max 12 lines.

## Result (owner, max 8 lines)
`pl-reviewer/T-270-finished-check` · docs only (no code, no test run needed) · `docs/plans/finished-check.md`: 40 ROADMAP rows and 78 BRIEF items, each with its proof on `main` 5974457; no proof is broken. Open: the release (U6, U11), the user's repair run (U7, U8), U12, row 34 (new right, U10: no), the §7 screenshot rule (27 of 51 UI PRs without one), no test of the install's login print, O1. ROADMAP rows 17, 19, 20, 35 … 39 and slices 8, 9 fixed. Stale texts for T-273: D-422 "proposed", D-341 and O3 `deepmerge`.

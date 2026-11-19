# T-264 – Small fixes from the release QA sweep (T-260)

- **Owner:** pl-reviewer (pl-backend is on the host)   · **Reviewer:** pl-backend   · **QA:** –
- **Size:** S   · **Depends on:** T-260 (#394)

## Goal
The lows of `docs/tasks/T-260.log.md` (O7 table) that `pl-lead` wants in the first release. The others are in `docs/plans/after-release.md` or dropped.

## Acceptance criteria
- [ ] Rows 38 and 39 (Block preview in the web): `"{{ .a }} {{- .b }}"` gives the same text as Go's template (the `after` capture in `fillTemplate`); `PARAM_REF` does not match inside string literals. Tests in `block-preview.test.ts`.
- [ ] Row 82 (T-247 log F2): after a Run stores a new digest, open `guest:config` drift Issues of that Target are compared again and the empty ones end with a system comment (D-428). An open Issue that is no longer true must not stay. Test; say what the poll does today and why it missed it.
- [ ] Row 74: when `openReenroll` fails with a store error, Plumb's reason is in the Issue (not only in the log); the Issue stays open (D-440). Test.
- [ ] Rows 61, 70, 81: the comment line at `guestDisks`; the README line "the disk gate (85 % by default)"; the two stale comment lines in `mcp_prompt_qa_test.go`.
- [ ] O9 (a): one README line that an older safety backup can stay until the next restore that works.
- [ ] Rows 74 and 82 touch the restore/Issue path: `pl-backend` reviews them as critical. `make lint test`; evidence in the PR first; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

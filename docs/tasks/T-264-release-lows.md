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
- `pl-reviewer/T-264-release-lows` · tests: `make lint test` rc 0 (dev VM); CI "lint, test, build" proves all of it, nothing skips locally.
- Rows 38/39: `fillTemplate` trims between actions in one scan (Go's result); literals are stripped before `PARAM_REF`. 2 new cases fail on the old code.
- Row 82: the poll compares a guest only when its digest changed, and a Run that fixed the drift stored the new digest, so the Issue stayed. Now a Run stores no digest while a `guest:config` Issue is open (`runDigest`): the next poll compares and ends it (D-428 comment). The test fails without the fix.
- Row 74: a store error in `openReenroll` puts Plumb's reason and the way out into the Issue (`reenrollNotMade`), "restored" state kept; no store text in the Issue. The test fails without the fix.
- Rows 61, 70, 81 and the O9 (a) README line: done.

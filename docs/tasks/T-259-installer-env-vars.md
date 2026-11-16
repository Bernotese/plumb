# T-259 – The release installer does not take its paths from the environment (finding of T-257)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer (installer = critical)   · **QA:** C9
- **Size:** S   · **Depends on:** T-257 (#387) merged

## Goal
`install.sh` reads 13 environment variables (`PLUMB_INSTALL_*`; the fixed list is in `install/hooks_test.go` and the #387 comment). Some change what root reads or writes, for example `PLUMB_INSTALL_PVE_CA`: Plumb would trust another CA. `sudo -E`, `env_keep` or a wrapper hands them to a root run without the user seeing it. They exist for the tests.

## Acceptance criteria
- [ ] First a table in the PR: each variable, what it changes, who needs it (tests only / a real user), and whether the README or `--help` names it.
- [ ] A **stamped release installer** (the one in a tarball) refuses to run when a `PLUMB_INSTALL_*` variable of the test-only kind is set: it names the variable and changes nothing (also in `--dry-run`). No silent unset.
- [ ] A variable that a real user needs (if any) becomes visible: the dry run and the real run print its effect in the summary ("CA file: … (from the environment)"), or it becomes a flag. A new or changed flag: say it in the report; `pl-lead` decides before the merge.
- [ ] The source installer (no stamp) keeps the variables for the tests, and says once that it is a source build.
- [ ] Tests: each variable against a stamped bundle → refused; the bundle tests still pass; the env allow-list test of #387 stays. Mutants. `make lint test`; PR to `main`; evidence in the PR first; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

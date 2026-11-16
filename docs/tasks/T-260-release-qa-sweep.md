# T-260 – QA sweep before the release (release slice C9)

- **Owner:** pl-reviewer   · **Reviewer:** pl-backend; `pl-lead` decides O9 (b)   · **QA:** –
- **Size:** M   · **Depends on:** T-257 (#387) for the mutant part

## Goal
Release slice, card **C9** of `docs/plans/release-slice.md` (rows O1, O7, O9, O11; section 5a "P2" and "P4"). Second opinion by tests on what the release slice built, and a real try to catch the firewall test flake.

## Acceptance criteria
- [ ] **O1, first:** `TestGuestFirewallProbeLoop` exactly as P2 (about 200 rounds inside one job, in the LXD container job and in the guest VM job, first after boot, a fresh listener and a connect probe with a 5 s limit that tells a timeout from a refusal, the T-250 dump on the first failure). No new job, no rerun loop. A failure with the dump → stop and report to `pl-lead` (a fix card). 0 of about 400 → say so; the release notes will name O1 as unexplained.
- [ ] **O7:** the list of lows that were dropped with a reason or left by the slice 8 reviews (sources: `docs/DECISIONS.md` D-415 and D-433, the review comments of the slice 8 PRs #360 … #384, `docs/STATUS.md`). For each: fixed already / a card is needed (propose it) / dropped with the reason. A table in `docs/tasks/T-260.log.md`; `pl-lead` decides the cards.
- [ ] **O9:** (a) the D-429 drop after a cut restore Run: a test that shows today's behaviour, and a proposal (fix now or leave with a line in the README). (b) a re-enroll Plan that survives a head move: only a written proposal with the risks (the D-439/D-440 gate, what "equal documents" must mean); nothing is built without `pl-lead`'s yes.
- [ ] **O11:** `TestAgentWithRealLink` and its lock test run 50 times in one go without a failure (say the command).
- [ ] **Mutants of the installer bundle check** (T-257, when #387 is merged; T-259 when it is merged): your own mutants, not the author's; list survivors.
- [ ] Test files only (and the log); a product fix is a card. `make lint test`; name the CI jobs that prove each part and what skips locally; PR to `main`; evidence in the PR first; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

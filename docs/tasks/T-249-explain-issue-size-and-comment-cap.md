# T-249 – explain_issue: a big Issue must not fail the prompt; dropped comments are marked (QA findings)

- **Owner:** pl-reviewer (author of T-240)   · **Reviewer:** pl-architect   · **QA:** the tests of PR #371
- **Size:** S   · **Depends on:** PR #371 (its tests show both)

## Goal
QA of T-240 (T-245 C4, PR #371) found two gaps in the MCP prompt `explain_issue`.

## Acceptance criteria
- [ ] F1: an Issue answer over 256 KiB (a big file drift) makes the whole prompt fail. The prompt is built anyway: the big parts are cut with a mark (`left_out` / "cut"), inside the fence, and the limits of the message hold.
- [ ] F2: comments before the last 20 are dropped without a mark. A silent cap is a way to push a person's warning out (20 short comments by a plan token). Say how many were left out, and keep comments of human admins before comments of tokens when something must go (or say why that is not possible); the same check for every other list the prompt reads (Runs, changes, events): no list is cut without a mark.
- [ ] Tests for both (the QA tests of #371 turn from "documents the gap" to "pins the fix"); mutants. No new API operation or right.
- [ ] `make lint test`; PR to `main`; report to `pl-lead`.

## Result (owner, max 8 lines)
branch · commit · tests · notes

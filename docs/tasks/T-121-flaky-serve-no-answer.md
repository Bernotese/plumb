# T-121 – Fix flaky `cmd/plumb TestServeNoAnswer`

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –   · **Parent:** T-107 (C11)

## Goal
The test failed once with "token refused" instead of "no answer". Find the race and fix the test or the code.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C11, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-121.md`; ask pl-lead for numbers

## Acceptance criteria
- [x] Cause named; a product bug is reported to pl-lead before a larger fix
- [x] 200 runs green; small diff; `make lint test build` green

## Out of scope
- Other tests
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
pl-codex-fix/T-121-serve-no-answer · see PR commit · 200 repeats and `make lint test build` pass.
Cause: closing the fake server releases its port; another test server can reuse it and return 401.
Fix: reserve the port and close accepted connections without a TLS or HTTP answer.
Test-only race; reported to pl-lead. No product change or new decision.

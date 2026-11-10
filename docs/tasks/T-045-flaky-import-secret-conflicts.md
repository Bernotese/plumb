# T-045 – Flaky test TestImportSecretConflicts

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
`TestImportSecretConflicts` is stable, and we know if its failure shows a product bug.

## Situation
Found by pl-architect in the PR #75 review: `internal/api/export_test.go:246` "secret conflict changed database content" fails 99 of 480 runs under load (8 processes × 40) on `main`, and 0 of 150 alone. The test compares a full DB snapshot around a refused import. Some asynchronous write lands in between.

## Acceptance criteria
- [ ] Name the write that lands in between (table, writer, trigger). Reproduce: `go test ./internal/api -run TestImportSecretConflicts -count=40` in 8 parallel processes.
- [ ] Decide with evidence: (a) the write belongs to the refused import → product bug against D-158 ("a 409/422 writes nothing"): fix the product and keep the strict test; or (b) it is unrelated background work (for example session touch, event log, link state) → make the snapshot exclude exactly that, by name, with a comment; do not loosen it further.
- [ ] After the fix: 0 failures in 480 runs under the same load. `make lint test` passes.
- [ ] Check the other snapshot tests of T-033 and T-037 ("every DB table unchanged") for the same pattern.
- [ ] Decisions, if any: `docs/decisions/T-045.md` (D-166).

## Out of scope
- Other flaky tests; report them in one line.

## Result (owner, max 8 lines)
branch · commit · tests · notes
pl-backend/T-045-flaky-import-secret-conflicts · tests: `go test ./internal/api -run '^TestImportSecretConflicts$' -count=60` × 8 parallel → 0/480 fail (before: 78/320); `make lint test` → pass.
Write in between: `agent.last_seen_at`, by `link.handlePoll` → `store.TouchAgent` on every poll of the fake agent (`env.agent`). All 156 diff lines were this column. Case (b): not import content; the import path never calls `Link.Send`. Fix: `qaImportDB` blanks only that column. D-166.
Other snapshot tests (`TestImportRefuses`, `TestQAImportRejectedDBUnchanged`, `TestQAImportApprovalBoundaryAndExactRoundTrip`, `TestSlice2TokenAndImportBoundaries`) use the same helper and get the same fix; on main they had 0/960 fails under the same load (window not hit). `link_test.dumpDB` is a leak check, not affected.

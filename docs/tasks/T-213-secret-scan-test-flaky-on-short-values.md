# T-213 – Flaky leak test: a 3-byte secret can occur by chance in sealed bytes

- **Owner:** pl-reviewer (secrets test)   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S (test-only)   · **Depends on:** after T-211

## Goal
The check job failed once in `TestSecretMaskedInAgentErrors/ⓩ`: the test scans the raw `plumb.db-wal` for a 3-byte secret; random sealed bytes can contain 3 given bytes by chance.

## Acceptance criteria
- [ ] The raw-file scan cannot fail by chance: needles in raw files are long enough (say the bound and the chance), short secrets are checked in the text columns / decoded outputs instead. No case is dropped: list where each old case is checked now.
- [ ] Sweep the other leak tests that scan raw files for short needles; fix the same way.
- [ ] `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

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
branch `pl-reviewer/T-213-short-secret-scan` · tests: `make lint test` → pass
- Bound: `noValue` scans the raw `plumb.db*` files only for needles of 8+ bytes (`rawScanMin`). A k-byte needle occurs by chance in an n-byte file of random bytes with about n/256^k: 3 bytes in a 4 MiB WAL ≈ 25 %, 8 bytes ≈ 2e-13.
- Shorter needles are looked for in the decoded TEXT values of every table (read-only connection; BLOBs with sealed data are skipped, they are the random bytes), and like every needle in events, API answers and the log.
- Old cases: `TestSecretMaskedInAgentErrors` `pw-canary-9x1` (13 bytes) stays in the raw scan; `ⓩ` (3 bytes) moves to the text scan + events + answers. All other `noValue` callers use 9+ byte values (raw scan unchanged). `TestQASecretValuesAndErrors` short modes (1–3 bytes) already skip the raw scan and check the stored step output (unchanged).
- Sweep of the other raw-file scans: `internal/secret` (26+ bytes), `internal/issue` flow (16) and lxc flow (tokens), `internal/backup` (fixed 15+ byte strings), `slice2Scan` (long canaries and their encodings), repair key scans (32-byte seed, base64), adopt e2e (14-byte mark): all far above the bound, no change.
- `TestNoValueTextScanFindsShortValue`: positive control (a token name holds the short value; the text scan finds it; a mutant that disables the text match fails it).

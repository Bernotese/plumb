# T-198 – Installer and controller agree on the range of the backup keep value

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex, ≤ ~40 lines)   · **Depends on:** T-174 merged

## Goal
Found in T-174: `install.sh` accepts `--backup-keep 1…999`, the controller refuses a value above 100 (D-361). An install with a larger value would write a setting that `serve` refuses.

## Acceptance criteria
- [ ] First say what happens today with `--backup-keep 500` (service fails to start? value ignored?) with file:line.
- [ ] One range in both places (the controller's), a clear installer error before any change on the host, a test in `install/`. Check the copy-timer keep the same way.
- [ ] `make lint test` passes; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-229 – Restore: the engine's own second check is as strict as the adapter's

- **Owner:** pl-backend (author of T-222)   · **Reviewer:** pl-reviewer (critical)   · **QA:** –
- **Size:** S (~10 lines + tests)   · **Depends on:** –

## Goal
Found by the QA of T-222 (PR #340, Q1) and confirmed by pl-reviewer: in `restoreVolume` the engine trusts `Kind = plumb` from the `GuestRestorer`; its own lookup `GuestBackupRunOfVolume` does not filter `deleted_at` and has no size/ctime. A fake or future driver, or an adapter bug, would pass a wrong volume, and a deleted-then-remade volume id passes the engine check alone.

## Acceptance criteria
- [ ] `restoreVolume` uses the recorded backups with deleted rows filtered and compares install id, lineage, trigger, size and ctime itself, before the guest is stopped.
- [ ] Tests with a fake restorer that says `plumb` for each mismatch (one per field, and the deleted-then-remade id): the Run refuses, nothing is stopped, nothing revoked.
- [ ] `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

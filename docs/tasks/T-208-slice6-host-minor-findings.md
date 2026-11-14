# T-208 – Slice 6 host run: minor findings F1–F4

- **Owner:** pl-backend   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S   · **Depends on:** T-207 first (same agent)

## Goal
The small findings of T-192 (`docs/tasks/T-192.log.md`).

## Acceptance criteria
- [ ] F1: `GET /settings` `proxmox.storages.backup` is always null: show the name the controller knows (from its config) or drop the field from the contract; say which and why.
- [ ] F2: `GET /backups` silently skips an unreadable backup file: list it with a state `unreadable` (no file content, no path outside the backup dir) and raise the existing backup Issue if there is one for this case.
- [ ] F3: a 422 repeats the same error 6 times: one message per rule and field.
- [ ] F4: the refusal text of `network` on a stock Ubuntu with a static address before its first reboot (D-192): one plain sentence that says what to do (reboot once), no internal words.
- [ ] Tests for each; `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

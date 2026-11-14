# T-171 – Users page: role hints match the real rights

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
The role hints in `web/src/screens/Users.svelte` say "backups" under `admin` and nothing on backup for `apply`; `POST /backups` is tier `apply` (found in the review of PR #270).

## Acceptance criteria
- [ ] The hints say what each role can do as the README table does after #270. If you can, build the hint from `roles.gen.json` (contract) so text and rights cannot drift; else a test that compares the hint with the contract for the backup operation.
- [ ] `make lint test` passes; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

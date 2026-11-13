# T-126 – Restore follow-ups (review of T-113)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-113   · **Parent:** T-107

## Goal
Close three small gaps that the review of PR #222 left.

## Context (read only these)
- D-311, D-318 … D-320 in `docs/DECISIONS.md`; the review comments of PR #222 (rounds 1–3)

## Acceptance criteria
- [ ] Locked users after a restore: an admin can give another user a new one-time password (the user must change it at the first login). Contract first (`api/openapi.yaml`), human-only, never a token; if this needs web work, tell pl-lead (pl-frontend takes it)
- [ ] A Target whose guest a person renamed in Proxmox: its delete behaves the same on a restored and on a never-restored controller once restored mode is over and no `guest:missing` Issue is open
- [ ] `ignore_once` on a `guest:missing` for a foreign guest on a bound VMID does not take the guest out of the unknown list
- [ ] Tests for each; `make lint test build` green; small diff

## Out of scope
- Adoption of guests
- No new token right (D-081)

## Result (owner, max 8 lines)
branch · commit · tests · notes

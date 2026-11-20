# T-127 – Plans that adopt guest data are human-only

- **Owner:** pl-reviewer (as implementer)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-125 (on main)

## Goal
A Plan that adopts data reported by a guest ("Accept" of an Issue, also "Accept as raw for this Target") can be approved only by a person, never by a token (user decision, 2026-11-13, D-323).

## Context (read only these)
- D-323 in `docs/DECISIONS.md`; the review of PR #218 (why: a compromised guest can report valid-looking values, e.g. an SSH key of a user or an apt source)
- The human-only rules that exist (approval path, risk classes; ARCHITECTURE "human-only") and `issue.AcceptRefused`

## Acceptance criteria
- [ ] Every Plan made by an Accept is marked human-only with the reason `accept-guest-data`; a token of any tier gets the existing human-only refusal at approve (and at any auto-approve path); a person's session can approve
- [ ] The mark is set by the server from how the Plan was made, not from a client field; it survives a re-proposal and cannot be dropped by editing the proposal
- [ ] Contract (`api/openapi.yaml`) and `docs/ARCHITECTURE.md` say it; the Plan view shows the reason (tell pl-lead if the web needs a text: pl-frontend)
- [ ] Tests: token of each tier refused, session allowed, mark kept on re-proposal; the existing Accept tests stay green
- [ ] `make lint test build` green; small diff

## Out of scope
- Revert and Ignore (they adopt no guest data)
- No new token right (D-081)

## Result (owner, max 8 lines)
branch · commit · tests · notes

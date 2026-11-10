# T-039 – Image builds: Blocks, seal, retention, schedule   (**blocked: test host**)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (will be split)   · **Depends on:** test host (D-041), T-012 and the agent PRs (#8, #13) on `main`

## Goal
Plumb builds its own templates from a base image plus Blocks, keeps versions, removes old ones and rebuilds on a schedule.

## Design (decision level – D-098, BRIEF §5.4, ARCHITECTURE §12)
- `Image` spec gets `blocks`, `retention.keep`, `schedule`.
- Build = Run: temporary guest → apply Blocks → agent `seal` → stop → template → image version row.
- A build never changes a target. A target records its image version.
- Retention keeps the newest `keep` versions and every version that a guest still needs.
- A scheduled build is planned by `system`. Its approval rule waits for the user's answer (T-025, question 2).

## Before this card starts
- [ ] `pl-architect` turns the design into a plan with the facts from the host: template conversion, linked and full clones per storage, what `seal` must reset on Debian 13 and Ubuntu 26.04, delete rules for templates with clones.
- [ ] Parts that need no host (schema, version table, retention choice, schedule clock) can become their own unblocked card then.
- [ ] Needs new architecture decisions.

## Out of scope
- Signed image checksums. Images for other distributions.

## Result (owner, max 8 lines)
branch · commit · tests · notes

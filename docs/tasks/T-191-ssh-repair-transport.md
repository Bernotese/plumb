# T-191 – SSH repair transport

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** T-195
- **Size:** L   · **Depends on:** T-190 merged

## Goal
Slice 6 part B, child card **B3a** of T-172. The transport of D-377: one Plumb repair key, a forced-command line in new guests, host key pins from the agent's report.

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` part B (as updated by T-189), contract `api/openapi.yaml` + `docs/ARCHITECTURE.md` §19, decisions D-372, D-373, D-377, D-378, D-382 … D-389 (until PR #290 is merged: branch `pl-architect/T-189-contract-b`), risks `docs/plans/risks.md` §2, §3 on branch `pl-reviewer/T-140-risk-draft`. Read row B3a, the test table of ARCH §19 (each rule names its test) and the review comments of PR #290.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row B3a lists; touch only the files that row gives you (test files next to them need no question; list them in the PR). Another file: ask `pl-lead` first.
- [ ] Every test that ARCH §19 names for this row exists under that name.
- [ ] Migration 0021. The private key: own sealed table, never in an export, API answer, log or unencrypted backup (leak test greps all four). `from=` with IP literals only; dial only the Target's static address / on-link subnet; a changed host key refuses before anything is sent.
- [ ] Review R2: rotation with explicit `revoke_old`, never by time. New dependency `golang.org/x/crypto/ssh`: exact version, govulncheck clean; fuzz for every parser of guest-controlled bytes (host key, SSH banner handling is the library's: bound time and size).
- [ ] CI guest test in the VM job: a dead agent is repaired; a changed host key is refused; the line allows nothing but the fixed command (try a shell, a port forward, scp).
- [ ] A card that adds an API operation updates `roleHints.ts`, the tier test and the contract marks in the same PR. No new token right. New decisions only with numbers from `pl-lead`.
- [ ] `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. The guest-agent transport (not built, D-377). Slice 7.

## Result (owner, max 8 lines)
branch · commit · tests · notes

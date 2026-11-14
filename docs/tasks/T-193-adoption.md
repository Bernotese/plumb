# T-193 – Adoption of guests in the pool

- **Owner:** pl-architect (implementer)   · **Reviewer:** pl-reviewer   · **QA:** T-195
- **Size:** L   · **Depends on:** T-189 merged; `propose.go` after T-190

## Goal
Slice 6 part B, child card **B4** of T-172. A guest that a person put into the pool `plumb` can become a Target (D-373, D-378). Plumb never moves a guest.

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` part B (as updated by T-189), contract `api/openapi.yaml` + `docs/ARCHITECTURE.md` §19, decisions D-372, D-373, D-377, D-378, D-382 … D-389 (until PR #290 is merged: branch `pl-architect/T-189-contract-b`), risks `docs/plans/risks.md` §2, §3 on branch `pl-reviewer/T-140-risk-draft`. Read row B4, the test table of ARCH §19 (each rule names its test) and the review comments of PR #290.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row B4 lists; touch only the files that row gives you (test files next to them need no question; list them in the PR). Another file: ask `pl-lead` first.
- [ ] Every test that ARCH §19 names for this row exists under that name.
- [ ] Adopt Plans are human-only; guest data of the adopted guest is inside the copy rule (D-331, D-359); `cipassword`, LXC env and other secrets are never stored or shown (redaction test).
- [ ] Delete of an adopted Target = unbind only, unless the Plan says `delete_guest` in clear words (human-only, destructive). The install command carries no token on the command line (R6). VMID reuse between Plan and Run is refused (digest check).
- [ ] A card that adds an API operation updates `roleHints.ts`, the tier test and the contract marks in the same PR. No new token right. New decisions only with numbers from `pl-lead`.
- [ ] `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. The guest-agent transport (not built, D-377). Slice 7.

## Result (owner, max 8 lines)
branch · commit · tests · notes

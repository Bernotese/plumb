# T-162 – Agent link: a store error must not look like "unauthorized"

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex, ≤ ~80 lines)   · **Depends on:** –

## Goal
Found in T-148 (PR #248, red race job): the link poll answers 401 on **any** error of `TouchAgent`, also `database is locked`. An agent that gets 401 treats its credential as refused. A busy database must give a retryable answer (503), and only a really unknown or revoked credential gives 401.

## Context (read only these)
- The link poll handler (search `TouchAgent` in `internal/link` / `internal/api`) and the agent side that handles 401 (`internal/agent`)
- PR #248 comment of pl-backend on the cause

## Acceptance criteria
- [x] First say in the PR what the **real** agent does on 401 today (drops the credential? retries? needs re-enrollment?). Facts from the code, with file:line.
- [x] Store errors other than "not found / revoked" → 503 with a retry; 401 only for a bad credential. No error text of the store in the answer.
- [x] Tests: a locked/failed store gives 503 and the agent keeps its credential and connects again; a revoked credential still gives 401.
- [x] Check the other link endpoints for the same pattern; fix the same way or list them in the report.
- [x] `make lint test` passes. Own worktree and branch, PR to `main`, report to `pl-lead`.

## Out of scope
- Changing how enrollment or revocation works.

## Result (owner, max 8 lines)
pl-codex-fix/T-162-link-poll-store-error · see PR commit · `make lint test` passed.
Real agent drops memory/disk credential on 401 and retries enrollment; fresh token required.
Poll TouchAgent and poll/report credential lookup now return generic 503 + Retry-After for store failures.
Real-agent/link test injects failed SQLite write, then verifies reconnect with the same credential.
Revocation remains 401; lookup failures tested on poll/report.
Other endpoints: enrollment distinguishes invalid token (401) from store failure (500); binary is unauthenticated.
Optional race check unavailable: no C compiler on VM. Latest origin/main merged before PR.

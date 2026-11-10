# T-037 – Slice 2 acceptance and secret leak hunt

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex; two PRs if the diff passes ~150 lines)   · **Depends on:** T-029, T-030, T-031, T-032, T-033 on `main`

## Goal
An independent test proves the part A flows over HTTP, and proves that a secret value appears nowhere outside the secret store and the agent link.

## Context (read only these)
- `docs/ARCHITECTURE.md` §12 · `docs/DECISIONS.md` D-089 … D-095, D-099
- `internal/api/env_test.go` (HTTP test harness to reuse), `docs/tasks/T-015-slice-acceptance.md` (style) · `internal/link/linktest/agent.go`

## Files
`internal/api/slice2_qa_test.go` · `docs/tasks/T-037.qa.log.md`

## Acceptance criteria
- [ ] One HTTP-only flow on the real controller with `driver/fake` and `linktest`: create token → set secret → target with a secret parameter → plan → approve → drift → ignore once → ignore by rule → replace secret → plan `targets` → export with passphrase → import into a second fresh controller → rollback.
- [ ] Leak hunt with a random canary as the secret value. After the flow, search: the SQLite file and its WAL, every API response, the full SSE stream, the captured `slog` output, the export without passphrase, the export with passphrase. Search for the canary as text, as base64 (standard and URL form) and as hex. Zero hits. The agent side must have the canary (positive control).
- [ ] The same hunt with `LeakSensitive` on the simulated agent: still zero hits on the controller side.
- [ ] Token checks: a `plan` token cannot call `PUT /secrets`, `/tokens`, `POST /export`, `POST /import` or `ignore_once`; an `apply` token cannot approve a `destructive` rollback.
- [ ] Human-only checks (D-099) with an `apply` token of max risk `disruptive`: it cannot approve a Plan that binds a secret to a new target or to another resource, and no Plan that adds, changes or deletes a `Rule`. Each case by plain proposal, by rollback and by import. It cannot `ignore_once` a sensitive change.
- [ ] Import over live secrets: without `overwrite` nothing changes and the answer is 409.
- [ ] No production code changes. A failure is reported to `pl-lead` with the test name, not fixed.
- [ ] `make lint test` passes; the flow test runs 10 times without a failure.
- [ ] Stays inside approved decisions.

## Out of scope
- Real agent, real Proxmox (T-015, T-021). UI tests (in the UI cards).

## Result (owner, max 8 lines)
branch · commit · tests · notes

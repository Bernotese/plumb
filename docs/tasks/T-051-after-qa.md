# T-051 – QA: first-Run proof for `nginx` + `nginx-site`

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** T-047, T-048, T-050   · **Parent:** T-044 (closes it)

## Goal
Tests prove that `nginx` and two `nginx-site` uses apply clean in the first Run, and that the skip rule holds.

## Context (read only these)
- `docs/tasks/T-044-block-order-nginx-site.md` – acceptance criteria, Plan section 8
- `docs/decisions/T-044.md` – D-170, D-174
- `test/slice/fixture_test.go`, `test/slice/slice_test.go`, `internal/agent/guest_test.go`, `test/guest/run.sh`

## Acceptance criteria
- [ ] **Slice test, no Docker (the proof of the race):** the fixture makes `/etc/nginx` only when the fake package install runs, with a delay. Path (a): a new Target with `nginx` + two sites succeeds in the first Run. Path (b), **S4:** an enrolled Target without nginx adds `nginx` + two sites; the Plan has real planned changes and the Run succeeds (`wire.Covers` after the wait). Both Target orders.
- [ ] Mutation, in the log: without `after` in the Block, the slice test fails.
- [ ] **Guest test in the CI guest job:** `TestGuestNginxSitesFirstRun` in `internal/agent` (tag `guest`). Purge nginx and check that `/etc/nginx` is gone; make two web roots with different pages; render the Target from `blocks.FS`; one `apply` with all resources `unverified`. No step is `failed`, `refused` or `skipped`; `curl -H "Host: a.lan"` and `b.lan` return each site's own page. Both Target orders (purge between). Raise `-test.timeout` of `agent.test` in `test/guest/run.sh`.
- [ ] Agent skip-rule audit: add tests that T-048 does not have (S1 no restart from an `after` edge, S5 context end during the wait, N1 output on every skipped step). Report a product bug to pl-lead; do not fix it here.
- [ ] `make lint test` and `sg docker -c "make test-guest"` pass; the CI guest job is green.
- [ ] Log: `docs/tasks/T-051-qa.log.md`.

## Out of scope
- Product code. A real-host test (Proxmox).

## Result (owner, max 8 lines)
branch · commit · tests · notes

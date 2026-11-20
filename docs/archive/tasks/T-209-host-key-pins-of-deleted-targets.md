# T-209 – Host key pins and repair reports of deleted Targets stay (host finding F10)

- **Owner:** pl-architect (pl-backend, the author of T-191, is on the host)   · **Reviewer:** pl-reviewer (repair path)   · **QA:** –
- **Size:** S–M   · **Depends on:** –

## Goal
Found on the real host (T-192 re-run, build f909ecc; fails closed): the rows `guest_host_key` and `repair_report` of deleted Targets stay. Effect: a guest that is adopted again under a new name gets no pin and repair is `off: host-key-shared, no-pin`, because its own keys are still pinned for the deleted Target. A reused VMID inherits stale pins of an older Target.

## Context (read only these)
- `docs/tasks/T-192.log.md` F10 (ask pl-backend for the lines, short) · ARCH §19, D-383 … D-389, D-393, D-396 · `internal/store/hostkey*.go`, `internal/repair/ssh_status.go`, the Target delete and unbind paths in `internal/plan/run.go`

## Acceptance criteria
- [ ] Deleting a Target (made by Plumb or adopted) and a confirmed unbind drop its host key pins and its repair report in the same transaction as the binding. A parked (unbind not confirmed) guest: say what happens to its rows and why (D-396).
- [ ] A new binding never starts with pins of another binding: pins are tied to the binding (generation), not only to the Target name or VMID; a reused VMID and a reused name both start clean. Test both.
- [ ] Migration 0024 removes the rows that belong to no current binding (the host has such rows today); test on a DB with stale rows; row counts of all other tables unchanged.
- [ ] `host-key-shared` still refuses a real clone (two live Targets with the same key).
- [ ] Sweep: every other per-Target table for the same leak (guest values, drift state, live_watch, rate buckets, foreign_guest, unbind_pending …): list each with "dropped at delete: yes/no, why" in the PR and fix the wrong ones.
- [ ] `make lint test`; PR to `main`; report. Proven on the host in T-192 (re-adopt 9100 → pin is set, repair ready).

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-136 – CI: the install tests got 2 minutes slower with T-132; the race job hits the 15 min limit

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-132 (on main)   · **Urgent: main and every PR are cancelled in "lint, test, build"**

## Goal
The CI job "lint, test, build" is green again on main.

## Evidence (pl-lead, 2026-11-13)
- main 575a62b (before T-132): `./install` 209 s in `make test`, 337 s in `make test-race`; job green.
- main bf42f61 (after T-132, PR #233): `./install` 334 s in `make test`; `make test-race` is cancelled at the 15 min job limit while `install.test` and a `bash` child still run. Same on PR #234 and on the docs-only PR #235.
- PR #233 itself was green (close to the limit).

## Acceptance criteria
- [ ] Find what costs the 2 minutes (likely real sleeps or waits of `wait_booted`, the retry after exit 129, or `push_checked` in the fake-PVE tests) and make it fast in tests without weakening the product (e.g. a test-only short interval through an env var the fake sets, a fake `systemctl` that answers at once). Say per test what it took before and after
- [ ] Rule out a real hang: a test that waits for ever must fail fast with a timeout of its own
- [ ] `./install` is back near the old time (target ≤ ~230 s plain); the race step ends well inside the job limit. Do not just raise the job timeout; if a higher limit is still right, say why
- [ ] `make lint test build` green; CI green on the PR

## Result (owner, max 8 lines)
branch · commit · tests · notes

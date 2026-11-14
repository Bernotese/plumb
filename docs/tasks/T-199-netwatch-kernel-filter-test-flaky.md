# T-199 – Flaky `TestNetReadOnlyKernel` in the VM job

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
The VM job of PR #282 was red: `TestNetReadOnlyKernel` (from T-181, on main) failed in 0.00 s, because kernel events of `TestGuestNetwatch` reached its socket first. pl-reviewer also found (review of #277, n1/n2) that the port-0 filter test skips where it cannot send netlink, and that no test covers ENOBUFS → trigger; both mutants survived.

## Acceptance criteria
- [ ] Read the red log first and confirm the cause in the PR (lines quoted).
- [ ] The kernel-only (port 0) filter is tested without a socket (pure function on message headers), so it runs in every CI job and cannot be disturbed by other tests. A test for ENOBUFS → trigger. The two mutants of the #277 review are killed.
- [ ] The socket test either goes or is made independent of events from other tests (own netns, or it tolerates foreign kernel events).
- [ ] `make lint test` + VM job green twice in a row; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

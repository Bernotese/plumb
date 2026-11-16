# T-250 – Find the cause of the TestGuestFirewall failure (second time, now with the T-200 dump)

- **Owner:** pl-reviewer   · **Reviewer:** pl-backend   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
`TestGuestFirewall` failed again (PR #374 at 9051d3a, job "Firewall in unprivileged LXD containers", job id 133015604094, ubuntu:26.04 container; first time: PR #281). This time the T-200 dump is in the log. Find the cause; do not just retry.

## Facts from the log (read it: `gh api repos/Bernotese/plumb/actions/jobs/133015604094/logs`)
- It fails at `guest_firewall_test.go:202`, `waitReach("8081", true)`, **before** the test builds the controller or the agent applies anything ("no apply returned yet"). So Plumb's firewall code has not run; the PR (restore code) is not the cause.
- `waitReach("8080", true)` passed just before. At the dump both 8080 and 8081 are "not reachable: exit status 124" (the probe timed out, not refused).
- The ruleset is empty but for the empty table `plumb_qa_foreign`; both listeners are in LISTEN on the veth address; the peer namespace sees the neighbour as REACHABLE.

## Acceptance criteria
- [ ] Say what makes the probe time out with no rule in the way: read how the listeners and the probe are made (one-shot listener? a probe that waits for the server to close? the 10 s deadline vs a slow container start?). Reproduce it locally if you can (the netplan/LXD notes in the memory of the team; else reason from the code and prove with a test of the helper).
- [ ] Fix the test helper so that it measures what it means (a TCP connect that works = reachable), keep the dump, and say in the PR why this is test-only – or, if it is a product fault, stop and report to `pl-lead`.
- [ ] `make lint test`; the CI job "Firewall in unprivileged LXD containers" proves it (local skips); PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

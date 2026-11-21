# T-276 – Guest tests: no hang on a slow outside mirror; the NIC test waits for the address

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** the two guest CI jobs
- **Size:** S   · **Depends on:** –

## Goal
Three red CI jobs on 2026-11-19/20 that were not caused by their PRs:
- `TestGuestNetworkNIC:113` (PR #411): one read of the main address right after `netplan apply`.
- `TestAptRepositoryInGuest` (PR #416) and `TestGuestInstallCommandFailsLoudly/no_curl` (PR #417): each ran into the 5 min package timeout while it waited for a package mirror outside the runner (`no_curl` took 104 s in the first run and 4m59s in the second).

## Acceptance criteria
- [ ] Say first what each of the two mirror tests needs from the outside (which apt call, which host) and whether the test's meaning needs the real mirror. Where it does not: no outside call (a local file repo in the guest, a pre-fetched package, or the check without the download). Where it does: a bounded wait with a clear skip-or-fail rule that is written down (a slow mirror must not look like a product failure, and a product hang must still fail), and the time in the log.
- [ ] `TestGuestNetworkNIC`: wait for the main address as `waitLive` does, log how long it was gone, and still fail when it stays away longer than a bound (say the bound and why).
- [ ] Is the short loss of the guest's main address at each `netplan apply` written down for users? If not: one README line (give it to T-275).
- [ ] Test files only. `make lint test`; both guest CI jobs green and their run times in the evidence comment; PR; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

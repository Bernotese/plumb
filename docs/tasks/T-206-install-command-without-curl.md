# T-206 – Install command of an adopted guest: no silent success without `curl` (host finding F6)

- **Owner:** pl-reviewer (implementer; repair path)   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
Found on the real host (T-192): a stock Debian 13 LXC has no `curl`, `wget` or `python3`. The install command `curl … | sh` then exits 0 and installs nothing. A person thinks the guest is adopted.

## Acceptance criteria
- [ ] The command fails loudly (non-zero, one clear line) when it cannot download, when the download is cut (the `main()` guard exists) and when the shell gets an empty script. No `curl | sh` shape that hides the exit code of `curl`.
- [ ] Without `curl`: say in the PR what a stock Debian 13 / Ubuntu 26.04 VM and LXC have, then choose: a fallback that needs no extra tool and keeps the CA pin (never a download without the pin), or a clear message "install curl first" plus the same line in the UI and README. Do not install packages silently.
- [ ] The one-time token is not wasted by a failed try before any byte was fetched (or say why it must be), and never appears on a command line or in the output.
- [ ] Tests in the CI guest job for: no curl, cut download, wrong CA. `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

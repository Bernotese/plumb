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
branch `pl-reviewer/T-206-install-command-no-curl` · tests: `make lint test` → pass; guest job: `TestGuestInstallCommandFailsLoudly`
- Cause: `curl … | sh` without curl → `sh` reads an empty script → exit 0. The token file was written before the download.
- Choice: no fallback without curl (no stock tool fetches https with only the Plumb CA pinned; `bash /dev/tcp` has no TLS), and Plumb installs no package: the command says "install curl first" in one `plumb-install:` line; the same line is on the Target page and in the README.
- New shape (`agentinstall.InstallCommand`, used by plan): a subshell (an error never ends the person's shell), `command -v curl`, download to a temp file with the CA pin and `--proto =https`, check the last line is the `main "$@"` guard (cut or empty → refused), only then the token here-doc, then `sh "$f"`. A failed try writes no token file; the token is used only at enrollment, so the same command can run again within 15 min.
- Stock images: the CI guest images (Debian 13, Ubuntu 26.04 base) have no curl; the host showed a stock Debian 13 LXC without curl, wget, python3 (F6). Cloud VM images were not checked here (T-192 can check).

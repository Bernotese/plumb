# T-278 – A container from a built template can miss its agent for ever: a boot race of two units (host finding, T-277)

- **Owner:** pl-reviewer (pl-backend is on the host)   · **Reviewer:** pl-backend (the template and the installer snippet = critical)   · **QA:** the guest CI jobs + T-277 on the host
- **Size:** S   · **Depends on:** –

## Goal
Found on the real host (T-277 a2b, controller RC4): an LXC made from a built container template came up without `/usr/local/bin/plumb-agent`. `plumb-agent.service` loops ("Unable to locate executable"), and the guest never enrolls: "the agent did not come online within 10m0s".

Cause: both `plumb-agent-install` and `plumb-agent.service` are enabled, both `After=network-online`, with no order between them. `plumb-agent.service` (Type=simple) is "active" for a moment after its fork, before the exec of the missing binary fails. The install script's first line is `if systemctl is-active --quiet plumb-agent.service; then exit 0`: it hit that moment, skipped the download, and never runs again. The units are unchanged since T-076; containers from this path came up in earlier host runs by timing. VMs are not hit (cloud-init orders the steps). The same script is in the installer's vendor snippet.

## Acceptance criteria
- [ ] The install step cannot be skipped while the binary is missing: the check is the binary AND the service (or the install unit is ordered `Before=plumb-agent.service` and the agent unit needs the binary: `ConditionPathExists`/`Requires`), so that no timing can leave a guest without its agent. Say which and why; the same in every copy of the script (ctpack, the vendor snippet; the test that keeps them equal stays).
- [ ] If the download fails (no network yet, a wrong token), the unit fails visibly and tries again in a bounded way; it never ends "ok" without a binary. What a user sees on the Target page in that case is true ("the agent did not come online": name where to look).
- [ ] A test that fails on today's units: the race forced (a fake `systemctl is-active` that says active once, or the real units in the container CI job with the agent unit started first), for Debian 13 and Ubuntu 26.04. A loop of first boots in the guest CI job (N times) with no miss.
- [ ] Existing built container Images keep the old units inside the template. Say what a user must do (a new Image version: rebuild), how Plumb shows a guest that hit the race (and that the SSH repair path fixes it: prove it in a test or say it does not), and put it into `CHANGELOG.md` and the README's known limits or upgrade notes.
- [ ] No new right. `make lint test`; the guest CI jobs green; evidence in the PR first; PR; report. The host proof (several containers from a freshly built template) is in T-277.

## Result (owner, max 8 lines)
branch · commit · tests · notes

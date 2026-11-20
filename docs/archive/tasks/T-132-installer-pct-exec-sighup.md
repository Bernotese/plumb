# T-132 – Installer: `pct exec` gets SIGHUP right after the first start of the LXC (T-122 F7), and the dry-run of a restore rerun (F6)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-130 (on main)

## Goal
An installer run does not die at random right after it made the Plumb LXC. A person on an SSH terminal would hit the same race.

## Context (read only these)
- `docs/tasks/T-122.log.md` F6, F7 (branch `pl-backend/T-122-slice4-real-host`); T-084 log (the same exit 129)
- `install/install.sh`: the first `pct start`, the binary push (`pct exec … mv`), the error path "cannot put /usr/local/bin/plumb in place"

## Acceptance criteria
- [ ] F7: find the cause as far as it can be proven without a host change (read-only on the host is allowed; a repro in a local container or the CI LXD job is better). Fix: after the first `pct start` the installer waits until the container has booted (bounded wait, clear error after it), and its `pct exec` calls do not depend on the controlling terminal of the run (no SIGHUP from a tty hangup inside the container). The key prompts keep their real TTY (D-180: the installer never captures the key)
- [ ] The step "put the binary in place" is safe to repeat: when the `mv` ran but `pct exec` reported a failure, the run checks the result (checksum) before it says "it was not changed"
- [ ] F6: `--restore --dry-run` on a host with a half-made LXC simulates the destroy, so it shows the token, ACL, create, push and restore steps and ends with exit 0
- [ ] Tests on the fake PVE (a `pct exec` that fails once with 129 after the first start; the dry-run of a rerun); shellcheck clean; `make lint test build` green
- [ ] Say in the PR what is proven and what stays a hypothesis

## Out of scope
- The host rerun (T-122, after pl-lead's go)
- No new token right (D-081)

## Result (owner, max 8 lines)
branch · commit · tests · notes

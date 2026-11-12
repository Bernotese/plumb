# T-083 – real-host acceptance of LXC Targets and QA audits

- **Owner:** pl-backend, pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-075 … T-081
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
LXC Targets are proven on `pve-dev`.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §10 (H1 … H11 without H10)
- AGENTS.md "Test host (Proxmox)"

## Acceptance criteria
- [ ] Every host check of §10 with its result in `docs/tasks/T-083.log.md` (no address, no token id, no key).
- [ ] From T-082 (review B1, moved H-B2): a full real LXC image build with the controller on `main`, Debian 13 and Ubuntu 26.04: build → seal → template → a Target cloned from the version boots, its agent installs and enrolls, first Run green; `ssh-keygen` runs before ssh (journal); no `env` in the template or the clone.
- [ ] The host is clean at the end.
- [ ] New decisions go to `docs/decisions/T-083.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- –

## Result (owner, max 8 lines)
branch · commit · tests · notes

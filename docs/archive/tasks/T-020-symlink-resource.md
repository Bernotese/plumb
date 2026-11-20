# T-020 – Resource type `symlink`

- **Owner:** pl-frontend (guest side)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S–M   · **Depends on:** T-005, T-018
- **Status:** started 2026-11-10 (T-005 and T-018 are on `main`; slice 1 is accepted without Proxmox)

## Goal
A Block can manage a symbolic link (e.g. `/etc/nginx/sites-enabled/<site>` → `sites-available/<site>`), because `file` and `directory` refuse every symlink in a path (D-054).

## Context (read only these)
- `docs/DECISIONS.md` D-054 and pl-architect's comment on PR #8.
- `internal/resource` (T-005).

## Acceptance criteria
- [ ] `symlink` resource: observe, diff, apply for link path + target; parent path is walked without following symlinks (same rule as `file`).
- [ ] Drift when the link is missing, points elsewhere, or is replaced by a file.
- [ ] Schema, risk class, tests (fake guest + guest run).
- [ ] The nginx Block can enable more than the default site.
- [ ] (added by pl-lead) Guest test as root in the CI guest job (Debian 13, Ubuntu 26.04): create, converge on the second apply, drift for the three cases, a symlinked parent is refused. Use `run_test` in `test/guest/run.sh`.
- [ ] (added by pl-lead) The link target is data, never followed: the resource does not read, write or chown through the link.
- [ ] (added by pl-lead) Decisions in `docs/decisions/T-020.md`, numbers D-162 … D-164. `make lint test` passes; CI incl. the guest job is green.

## Out of scope
- Hard links.

## Result (owner, max 8 lines)
branch · commit · tests · notes

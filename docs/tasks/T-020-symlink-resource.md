# T-020 – Resource type `symlink`

- **Owner:** tbd (guest side)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S–M   · **Depends on:** T-005, T-018
- **Status:** backlog (after the first slice; not started)

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

## Out of scope
- Hard links.

## Result (owner, max 8 lines)
branch · commit · tests · notes

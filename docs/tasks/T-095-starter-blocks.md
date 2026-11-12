# T-095 – Starter Blocks ssh-hardening, common-tools, shell, docker-compose

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-093   · **Parent:** T-089 (C5)

## Goal
Four new starter Blocks so a typical homelab guest can be built.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-095.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [ ] `ssh-hardening`, `common-tools`, `shell`, `docker-compose` in `blocks/`, valid against `schema/block.json`
- [ ] `docker-compose`: Docker's official apt repo with the key pinned in the Block; compose file as `file`; secrets only in a separate `.env` `file` resource with secret markers, mode 0600 (D-099); a `unit` runs `docker compose up -d`; supports `vm` (D-262); no `command` resource in it
- [ ] `docker compose` output is not logged in clear
- [ ] `ssh-hardening` must not lock out the Plumb SSH keys
- [ ] Guest tests on Debian 13 and Ubuntu 26.04 incl. a real compose app
- [ ] `make lint test build` and `make test-guest` pass

## Out of scope
- `lxc` for `docker-compose` (only after T-100 shows it works without new rights)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

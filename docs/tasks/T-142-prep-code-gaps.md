# T-142 – Prep: gap list BRIEF §5.4 … §5.9 against the code

- **Owner:** pl-codex-qa   · **Reviewer:** pl-architect (uses it in T-138)   · **QA:** –
- **Size:** S (plan file only, short tables)   · **Depends on:** –

## Goal
One checked table of what the brief asks and the code does not do yet.

## Context (read only these)
- `docs/BRIEF.md` §5.4 … §5.9 · `docs/DECISIONS.md` D-326
- Agent resources (`internal/agent/…`), starter Blocks, `api/openapi.yaml`

## Acceptance criteria
- [ ] `docs/plans/code-gaps.md` on your branch (own worktree, never the shared checkout), max ~80 lines:
  - §5.6 resource list (file, template, directory, package, apt repository, service, systemd unit, user, group, SSH key, sysctl, network, firewall, timer/cron, command, Docker Compose): exists yes/no, kinds (VM/LXC), has a real-guest test yes/no.
  - §5.7 starter catalogue (users, network, SSH, MOTD, shell, common tools, Nginx, Docker Compose): exists yes/no.
  - §5.4 (retention, scheduled rebuilds, LXC templates), §5.5, §5.8 (strictness, ignore by rule), §5.9 (event stream, tiers, provenance), §5.3 (export/import, rollback to a revision): done / partly / missing, with the file or route that proves it.
- [ ] Every "done" names the proof (file:line or test name). No guesses: write "not checked" if you did not look.
- [ ] Push the branch, no PR. Report to `pl-lead` (max 8 lines).

## Out of scope
- Code changes, new tests.

## Result (owner, max 8 lines)
branch · commit · tests · notes

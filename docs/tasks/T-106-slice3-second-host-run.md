# T-106 – Second short host run: T-102 … T-105 on the real host

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-102 … T-105 (on main)   · **Parent:** T-089

## Goal
Bring the test controller to the latest `main`. Then prove the fixes T-102 … T-105 on the real test host (user decision, 2026-11-13).

## Context (read only these)
- `docs/tasks/T-100.log.md` – the first run, findings F1/F2 and Q4
- The cards `T-102`, `T-103`, `T-104`, `T-105` – only goal and acceptance criteria
- `AGENTS.md` "Test host (Proxmox)"; your memories on the host (TTY, action limits, T-100 lessons)

## Acceptance criteria
- [ ] Allowed host actions: bring the controller in LXC 100 to the latest `main` (dry-run first, send the result to pl-lead, wait for the go); make test guests in the pool `plumb` (VMIDs 9000–9099) and remove them at the end. Nothing else without asking pl-lead
- [ ] Step 1: the controller runs the latest `main`; the old Issues and data are intact
- [ ] T-102: an agent update by Plan on a VM and an LXC where the update result gets lost at the swap; the Run ends `ok` on the hellos. Announce a controller-down or network test to pl-lead first and revert it at once
- [ ] T-103 + T-104: one Plan applies `docker-engine` and `docker-compose` (new unit + service in the same apply) on a fresh VM and a fresh unprivileged LXC (nesting only, no `keyctl`); a container runs in both
- [ ] T-105: a Block with a stopped + enabled service whose unit is absent; the Plan shows `enabled null→true`
- [ ] Log in `docs/tasks/T-106.log.md` (PR) without addresses, token ids, MACs, keys; host clean at the end (pool `plumb` and `plumb-images` empty)
- [ ] A new finding: record it in the log and report it; do not fix code in this card

## Out of scope
- New token rights; code changes; anything AGENTS.md "Test host" forbids

## Result (owner, max 8 lines)
branch · commit · tests · notes

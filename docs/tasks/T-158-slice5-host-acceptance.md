# T-158 – Slice 5 acceptance on the real host

- **Owner:** pl-backend (the one agent on the host)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-157)
- **Size:** M   · **Depends on:** T-146 … T-156 merged

## Goal
Slice 5, child card **C13** of T-138. Prove slice 5 on `pve-dev`.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan", decisions D-327 … D-347 (`docs/DECISIONS.md`; until merged in `docs/decisions/T-138.md`, `T-144.md`), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §17. Read row C13 of the child card table, the contracts, the risks, and the review comments of PR #241 / #243 that name this row.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C13 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] The 8 steps of "Real-host acceptance" in the plan, nothing else. Installer: `--dry-run` first, result to `pl-lead`, wait for the go. No change to network config, SSH config, `local`, `local-lvm`, rights. Test guests stay in the pool and are removed at the end.
- [ ] Log `docs/tasks/T-158.log.md` without addresses, MACs, token ids, keys, passwords; raw output redacted, 0600, outside the repo. Findings F1… to `pl-lead` at once.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-158.md`.
- [ ] No new token right. Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Allowed host actions
- Repair/update run of the installer to the latest `main` (dry-run first, then wait for the go of `pl-lead`).
- Through Plumb only: Images, Targets (VM and LXC) in the pool `plumb`, Blocks, users, tokens, Plans; delete them all at the end. One hand change inside a test guest per step that asks for drift (`ip addr del`, `groupdel`).
- Read-only checks on the node (`pvesh get`, `pveum … list`, `cat` of config) for the before/after proof (7 ACL lines, `storage.cfg` unchanged).
- Nothing else. No change of rights, network, SSH, storages. The controller address for step 5 is used only as a route target inside a test guest.
- One-time passwords, user passwords and tokens of the test users go to a 0600 file outside the repo; write only "stored in <path>".

## Also prove (found during slice 5)
- The agent link of a guest survives a `network` apply with the **real** controller (CI measured a ~25 ms route gap; say: reconnect yes/no, any Issue).
- A reboot of a test VM inside the safe-apply window: `plumb-agent-network.service` puts the confirmed state back (D-358).
- Catalogue on the live DB: the Block list before/after "Add"; stored Blocks of slice 4 are not overwritten.
- Migrations 0017, 0018, 0019 on the live DB with data: backup first (`plumb backup now`), then the row counts of plans, users, tokens before = after.

## Out of scope
- Everything of other rows. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

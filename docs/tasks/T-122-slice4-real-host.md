# T-122 – Slice 4 real-host acceptance

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** T-112 … T-119   · **Parent:** T-107 (C12)

## Goal
Prove slice 4 on the real test host.

## Context (read only these)
- `docs/tasks/T-107-slice-4-plan.md` – the approved plan: row C12, "Token rights", "Risks"; decisions D-287 … D-296 in `docs/DECISIONS.md` (accepted by the user, 2026-11-13)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-122.md`; ask pl-lead for numbers

## Acceptance criteria
- [ ] Only the host actions of the section "Allowed host actions" below. Dry-run first, then wait for the go
- [ ] Backup → off-host copy → `--remove --keep-guests` with a VM and an LXC in the pool (a wrong recovery key stops it before any change; a kept VM starts between remove and restore) → `--restore` (a wrong pool is refused) → restored mode, confirm, agents come back, Plan clean, revoked token stays off, `guest:missing` and unknown guests shown
- [ ] power / tags / grow on VM and LXC; a `firewall` change that cuts the link rolls back by itself; a `drop` without SSH is refused
- [ ] Recovery key handling per AGENTS.md (D-180); log `docs/tasks/T-122.log.md` without addresses, token ids, keys; host clean at the end

- [ ] Also prove on the host: the confirm check (a foreign guest on a reused VMID is marked, shown as unknown and never deleted; T-126), the one-time password for a locked user, `plumb backup verify` on an off-host copy
- [ ] A finding: record it in the log and report it; no code fix in this card

## Allowed host actions (pl-lead, 2026-11-13)
Only these. Anything else: ask pl-lead first. Dry-run of every installer path first, send the result, wait for the go.
1. Bring the controller in LXC 100 to the latest `main` (repair run).
2. Backup storage for the test host (proposal accepted for this host): one new directory storage `plumb-backup` on a new host path of its own (not on `local`, not on `local-lvm`; do not change those two). Make it as root on the host, then the repair run with `--backup-storage plumb-backup`. It stays after the run; it is part of the test install from now on.
3. Test guests in the pool `plumb`, VMIDs 9000–9099 (VM and LXC); removed at the end.
4. One guest made by hand as root (not by Plumb) in the pool `plumb` on a VMID that a deleted Plumb guest had, for the "foreign guest on a reused VMID" proof. Removed at the end by hand.
5. `--remove --keep-guests`, then `--restore` of the test controller, at the same address. First the proof that a wrong recovery key stops before any change. Recovery key: as in AGENTS.md (D-180), file `~/.config/plump/testhost-recovery.key`. The new admin password that the restore prints: into a 0600 file next to it, never into the log, the PR or a report.
6. A second restore proof with a deliberately wrong pool is a refusal test only: it must change nothing.
7. Firewall tests that cut the link: inside the test guests only; announce them to pl-lead; the controller stays up.
8. At the end: only LXC 100 (the restored controller, on `main`), pool `plumb` and `plumb-images` empty, the old Issues and history intact (compare a digest before and after), `plumb-backup` holds the copies.
Not allowed: network config, SSH config, `authorized_keys`, root password, `local`, `local-lvm`, reboot, upgrade, other machines, new token rights.

## Out of scope
- New token rights; anything AGENTS.md "Test host" forbids
- No new token right (D-081)

## Plan (architect, optional)

## Result (owner, max 8 lines)
- Branch `pl-backend/T-122-slice4-real-host`; log `docs/tasks/T-122.log.md`. Host: only LXC 100 (`bf42f61`) left, pool and `plumb-images` empty, `plumb-backup` 3 copies, old Issues digest unchanged.
- PASS: backup → off-LXC copy → keep-guests (wrong key stops; kept VM starts) → restore (wrong pool refused; 3rd run after T-130, T-132) → restored mode, confirm, agents back, Plan clean, revoked token off, `guest:missing`, foreign guest marked and kept (also after its Target delete).
- PASS: power/tags/grow on VM and LXC incl. retried root grow; firewall rollback when the link is cut (new connections dropped, 90 s); `drop` without SSH impossible without `allow_no_ssh` (with `ssh_port` refused); `backup verify` of an off-LXC copy.
- Not provable: one-time password for a locked user (F3: no second user can exist).
- Findings: F1, F2 (T-128), F4 (T-129), F5 (T-130), F6, F7 (T-132) fixed; open: F3, F8 (open `guest:missing` of deleted Targets), F9 (422 text for `allow_no_ssh` + `ssh_port`).

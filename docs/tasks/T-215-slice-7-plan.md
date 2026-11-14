# T-215 – Plan of slice 7 ("More of Proxmox": more NICs + VLAN, guest backups)

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer (new token rights, ACLs, installer, backup secrets = critical)   · **QA:** –
- **Size:** L (plan only, no code)   · **Depends on:** –

## Goal
Slice 6 is done (2026-11-15). By D-326 the next slice is planned at once. The user answered U3 and U4 (D-379, D-380), so slice 7 has its rights.

## Context (read only these)
- Your draft `docs/plans/slice-7-draft.md` (branch `pl-architect/slice-7-draft`) · host facts `docs/plans/host-facts-slice-7.md` (branch `pl-backend/T-214-host-facts-slice-7`) and `docs/plans/host-facts.md` (branch `pl-backend/T-139-prep-host-facts`) · `docs/plans/risks.md` §6 (branch `pl-reviewer/T-140-risk-draft`)
- `docs/DECISIONS.md` D-326, D-379, D-380, D-394 … D-397 · `docs/plans/ROADMAP.md` · `docs/tasks/T-192.log.md` (lessons)

## Acceptance criteria
- [ ] `docs/plans/ROADMAP.md` updated: slice 6 rows done; the open review notes and follow-ups of slice 6 placed (sweep the review comments of #272 … #320; e.g. `unbind_pending` STRICT, the template DB for `issue`/`store` tests, the left-behind view, an optional time test of the late-unbind memory) or dropped with a reason.
- [ ] The plan in this card, same form as T-172: scope, contracts, child cards with owner, **reviewer per D-394** (critical parts → pl-reviewer; others → a free agent, never the author), size, dependencies, shared files with an order, migration numbers (next: 0025, or 0026 if T-212 … took one – check), risks, real-host acceptance.
- [ ] The new rights exactly as D-379 / D-380 say, never more: `SDN.Use` per named bridge / tag path without propagation; `VM.Backup` on `/pool/plumb`, `Datastore.AllocateSpace` + `Datastore.Audit` on one named backup storage. The installer change, its dry-run text and the ACL view in Settings show each new line. On `pve-dev`: the card for `vmbr9` (VLAN-aware, no port; the only allowed network change, D-379) with the exact stanza and the undo.
- [ ] **USER points** at the top of the card for `pl-lead` (D-326 (4)): U7 (the repair run of the user's own install for the new ACL lines; the user's bridges and backup storage) and anything else that touches the user's own installation or needs a right beyond D-379 / D-380. Never assume an answer.
- [ ] Lessons of slice 6 as rules: every new command type goes through the real link in a test; every per-Target table says what happens at delete, unbind and VMID reuse (D-397); a host step for each human-only rule with a token; local green is not proof where tests skip (netplan, root, race): name the CI job that proves each card.
- [ ] Decisions with your recommendation in `docs/decisions/T-215.md` (numbers D-398 … D-415). PR to `main` (docs only); report.

## Out of scope
- Code. Any release step. Slice 8.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-204 – Adoption: a refused adopt Run leaves a stale Target that claims the VMID (host finding F5)

- **Owner:** pl-architect (author of T-193)   · **Reviewer:** pl-backend (general) + pl-reviewer (guard part)   · **QA:** –
- **Size:** M   · **Depends on:** –

## Goal
Found on the real host (T-192, build c369283): adopt Plan for VM 9100 → the guest is renamed in Proxmox → the admin approves → the Run refuses `guest-changed` and binds nothing (right). But the revision is committed: the Target document stays (phase pending, `adopted {vmid 9100}`, no binding). A second adopt under the new name gives a second document that claims the same VMID. Any later Plan for the stale Target walks the "bind a guest" path again.

## Context (read only these)
- `docs/tasks/T-192.log.md` F5 (worktree/branch of pl-backend; ask him for the lines) · ARCH §19, D-373, D-386, D-391, D-392 · `internal/plan/adopt.go`, `propose.go` (the bind path), `run.go`

## Acceptance criteria
- [ ] Say first how other Plans behave when the Run fails after the approval (a normal Target create whose clone fails also leaves a document: is that the same case and how is it shown?). Then decide and write as a decision (number from `pl-lead`): either a refused adopt commits nothing, or the stale adopted Target is a clear state.
- [ ] Hard rules whatever you choose: (1) two Target documents can never claim the same `adopted.node/vmid` (refused at save and at Propose); (2) a pending adopted Target without a binding can only be bound by a new **human-approved** adopt Plan that passes all checks again (name, pool, digest), never by an unrelated Plan (a param change, a Block update, a token Plan); (3) deleting it needs no Proxmox call and can never touch the guest; (4) the UI/API shows it as "not adopted: the guest changed" with the two actions (adopt again, remove).
- [ ] Tests for the host sequence (rename between Plan and approve; adopt again under the new name; a later unrelated Plan for the stale Target; delete of the stale Target) and mutants for rules (1)–(3).
- [ ] `make lint test`; PR to `main`; report. The fix is proven again on the host in T-192.

## Out of scope
- Moving guests (D-378).

## Result (owner, max 8 lines)
branch · commit · tests · notes

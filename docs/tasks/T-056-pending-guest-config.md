# T-056 – Pending Proxmox changes: seen at once, never left behind

- **Owner:** tbd (proposal: pl-backend; the web part is small, pl-frontend or pl-codex-fix)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M (Claude)   · **Depends on:** T-012 and T-053 on main   · **State:** planned (card only); one user question, see "Decisions"

## Goal
A config change that waits in Proxmox for the next cold start is drift at once, and the Issue says that it is pending. After a Run of Plumb, the running guest has the desired values: no value waits.

## Context (read only these)
- `docs/tasks/T-015.log.md` on `origin/pl-backend/T-015-real-host` – "Real-host run 1" and "Test change after run 1".
- `internal/driver/proxmox/driver.go` – `config` (`current=1`), `specOf`, `Observe`, `Update`, `checkUpdateParams`.
- `internal/driver/driver.go`, `diff.go` – `GuestState`, `DriftGuest`, `DiffGuest`, `guestRisk`.
- `internal/plan/guest.go` (`CheckGuests`), `internal/plan/run.go` (`guestStep` case `update`), `internal/plan/propose.go` (`planRendered`).
- `internal/wire/wire.go` (`Change`, `AttrRestart`), `api/openapi.yaml` (`Change`), `internal/issue/issue.go` (accept).
- `web/src/ui/ChangeTable.svelte`, `web/src/lib/view.ts`, `web/src/lib/mock/mock.ts`.
- D-063, D-078, D-185.

## Facts
1. Proven on the host (T-015 run 1): `memory` is not in the default `hotplug` list of Proxmox (`network,disk,usb`). `qm set --memory` on a running VM is stored as pending. `GET …/config?current=1` gives the running values; without `current` it gives the values after the next cold start. The digest covers both.
2. The driver reads only `current=1`. A pending change from outside is not seen until a cold start.
3. **From the code, not proven on the host:** the same is true for Plumb's own change. `Update` writes `cores` or `memory`; on a running VM that is pending. The Run does not restart the guest (only a re-enrollment does) and ends "applied". The Issue of a revert is then "resolved" while the VM still runs with the old value. In T-015 run 2 the revert of `memory` passed in 0.7 s this way.

## Plan (architect)

### Rule (D-190, D-191)
- `Observe` reads both views. `GuestState.Spec` is the running view, as today. The new `GuestState.Pending *GuestSpec` is the view after the next cold start; it is nil when both are equal.
- A guest matches only when the running value **and** the pending value are equal to the desired value. `DriftGuest` (the one rule of all drivers, D-063) reports per attribute:
  - running ≠ desired: `{from: running, to: desired}` as today.
  - running = desired, pending ≠ desired: `{from: pending, to: desired, pending: true}`.
- `wire.Change` gets `pending` (bool, left out when false). It means: `from` is set in the hypervisor but not active yet.
- Issue and Plan text for a pending change (web): tag `pending` on the row, and the line "Set in Proxmox but not active. The VM uses <to>. <from> becomes active at the next cold start."
- **Revert** of a pending-only change removes the pending value. The running guest does not change, so the risk is `reversible` and there is no restart.
- **Accept** works as today: the desired value becomes `from` (the pending value). The Plan of the accept then is a normal update of that attribute.
- **A Run never leaves a pending value.** After `Update` the engine observes again. If a managed value is still pending and the guest runs, the Run does a cold restart (`Power restart`, D-078) and observes once more. If the value is still pending, the step fails. A stopped guest is not started.
- The Plan shows that restart: an update of `cores` or `memory` on a running guest gets the `restart` change (`wire.AttrRestart`, risk `disruptive`) on `guest:config`. A pending-only revert gets none. If the guest runs at the Run but the Plan has no `restart` change (it was stopped at plan time), the step fails with "the guest runs now; plan again".

### Steps
1. `internal/driver/driver.go`, `diff.go`: `GuestState.Pending`; `DriftGuest` with the rule above; `DiffGuest` gives a pending-only change of a mutable attribute the risk `reversible`. A pending-only change of a create-only attribute stays `destructive` drift and a `*CreateOnlyError` in `Diff`.
2. `internal/wire/wire.go`: `Change.Pending`. `api/openapi.yaml`: `pending` in the non-sensitive `Change` branch. Mock: the same field.
3. `internal/driver/proxmox/driver.go`: `Observe` reads `config?current=1` and `config`. Both answers must have the same digest; if not, read again (3 tries, then an error). `Pending = specOf(next)` when it differs from `specOf(running)`. `Update` keeps writing the desired value (see host check 2).
4. `internal/driver/fake/fake.go`: model "running" and "pending" for `cores` and `memory` of a running guest; `Power restart` applies pending values; a test helper sets a pending value from outside.
5. `internal/plan/propose.go`: add the `restart` change by the rule above (the observed `Power` is known there).
6. `internal/plan/run.go`: after `Update`, the restart rule above. Keep the restart of the re-enrollment.
7. `internal/plan/guest.go`: no rule change; the digest already moves with a pending change (fact 1). Check it with a test.
8. `internal/issue/issue.go`: accept uses `from` as today; add a test for a pending change.
9. Web: `ChangeTable.svelte` and `view.ts` show the tag and the line; component test; `browser-shot` light and dark for the review.

### Tests
- `internal/driver` (`diff_test.go`): the table of running / pending / desired for a mutable and a create-only attribute; risks.
- `internal/driver/proxmox` with the fake PVE. The fake must model: `POST config` on a running VM puts `cores`, `memory` and `smbios1` into pending and applies `net0`; `GET config` with and without `current`; a write of the running value removes the pending entry; stop + start applies pending values; the digest moves on each of these.
  - Outside pending `memory` → `Observe` has `Pending`, `Spec` is the running value.
  - `Update` back to the desired value → `Pending` is nil, no power call.
  - Digest differs between the two reads → retry, then an error.
- `internal/plan` with `driver/fake`:
  - Outside pending change → `CheckGuests` reports `differs` with `pending: true` in one round.
  - Revert Plan: risk `reversible`, no `restart` change, the Run sends no power action, the next round reports `unchanged`.
  - Accept: the Target override has the pending value; the Plan has `restart`; the Run restarts; nothing is pending after it.
  - Plumb's own update of `memory` on a running guest: the Plan has `restart`, the Run restarts, `Observe` shows the new running value. On a stopped guest: no restart and no start.
  - A value that is still pending after the restart fails the step.
- Real host (in the T-015 acceptance run):
  1. `GET config` and `GET config?current=1` after `qm set --memory` on a running VM: values and digest as in fact 1.
  2. A write of the running value removes the pending entry (`qm pending` shows none). If not: `Update` sends `revert=<key>` for a pending-only change, and `checkUpdateParams` allows `revert` only with `cores`, `memory`, `net0`.
  3. The first form of the test step (no `qm reboot` after `qm set --memory`): the Issue with `pending` opens within 90 s; revert closes it; `qm pending` shows nothing; the boot ID is the same.
  4. A Plan that changes `memory`: the guest gets a new boot ID and `free -m` shows the new size.

## Acceptance criteria
- [ ] A pending change of `cores` or `memory` from outside opens an Issue within one poll round (60 s), marked pending.
- [ ] Revert removes it without a restart. Accept and Plumb's own update end with the value active.
- [ ] After every successful Run on a running guest, `Observe` shows no pending managed value.
- [ ] A Plan that will restart a guest says so before the approval.
- [ ] Tests: `make lint test build` passes; `make test-pve` passes on the test host with host checks 1 to 4.

## Out of scope
- Hot-plug of memory or cores (hotplug and NUMA settings).
- Revert of a pending cloud-init input (for example `ipconfig0`): the Issue shows it as pending, a person removes it on the host (D-185 A: the driver writes no cloud-init input).
- LXC guests (T-038).

## Decisions
`docs/decisions/T-056.md` (D-190, D-191). **User question (one):** may a Run cold-restart a running VM when the Plan changes `cores` or `memory`? Recommendation: yes; the Plan shows the restart before the approval. The other way is "the value waits until someone restarts the VM"; then Plumb needs a "waits for a restart" state in Issue, Target view and API, and the user must restart by hand. Today's behaviour is neither: the Run says "applied" and nothing is active.

## Result (owner, max 8 lines)
branch · commit · tests · notes

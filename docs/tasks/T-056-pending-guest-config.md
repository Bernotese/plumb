# T-056 – Pending Proxmox changes: seen at once, never left behind

- **Owner:** tbd (proposal: pl-backend; the web part is small, pl-frontend or pl-codex-fix)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M (Claude)   · **Depends on:** T-012 and T-053 on main   · **State:** planned (card only). The parts marked **[R]** wait for the user's yes to the restart question (see "Decisions"); with a yes the card needs no change. The other parts can start now.

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
- `Observe` reads both views. `GuestState.Spec` is the running view, as today. The new `GuestState.Pending *GuestSpec` is the view after the next stop and start; it is nil when both are equal.
- A guest matches only when the running value **and** the pending value are equal to the desired value. `DriftGuest` (the one rule of all drivers, D-063) reports per attribute:
  - running ≠ desired: `{from: running, to: desired}` as today. If a third value waits (pending ≠ running and pending ≠ desired), the change also has `also_pending: <value>`.
  - running = desired, pending ≠ desired: `{from: pending, to: desired, pending: true}`.
- `wire.Change` gets two optional fields. `pending` (bool): `from` is set in the hypervisor but not active yet. `also_pending` (a value): `from` is the running value and this other value waits. Both are for display; no rule reads `also_pending`.
- Text for a pending change (web, Issue and Plan): tag `pending` on the row, and the line "Set in Proxmox but not active. The VM uses <to>. <from> becomes active at the next stop and start of the VM. A reboot inside the guest is not enough." With `also_pending`: "Also pending: <value>."
- A pending create-only input (for example `ipconfig0`) is shown the same way, with: "Plumb does not revert this value. Remove it in Proxmox: use Revert on the pending value, or `qm set <vmid> --revert <key>`."
- **Revert** of a pending-only change of `cores`, `memory` or `bridge` removes the pending value. The running guest does not change. So the risk is `reversible`, not `disruptive`, and there is no restart. **This changes D-063** (every mutable attribute was `disruptive`): a token with `max_risk: reversible` can now revert a pending change that someone made outside Plumb (review S3).
- **Accept** works as today: the desired value becomes `from`. For a pending-only change that is the pending value, and the Plan of the accept is a normal update of that attribute.

**[R] A Run never leaves a pending value** (needs the user's yes):
- After `Update` the engine observes again. If a managed value is still pending and the guest runs, the Run restarts the guest with `Power restart` (D-078) and observes once more.
- What that restart is (review S4): a clean shutdown; if the guest is not off after 120 s (`ShutdownTimeout`), Proxmox powers it off hard (`forceStop`); then a start. The timeout has no setting (D-182). A Go test pins the default to 120 s, because the Plan text names it. If it becomes a setting, the `restart` change must carry the value.
- The Plan shows it: an update of `cores` or `memory` on a running guest gets the `restart` change (`wire.AttrRestart`, risk `disruptive`) on `guest:config`. Plan text of that change: "Restart of the VM if Proxmox cannot apply the change live: clean shutdown, hard power-off after 2 minutes, then start." A pending-only revert gets none.
- The restart may not happen (review N4): with memory or CPU hotplug nothing waits after `Update`, and the Run skips the planned restart. That is not an error.
- One restart only (review N2): a Plan with a re-enrollment and a change of `cores` or `memory` writes all values in one `Update` and restarts once.
- If the guest runs at the Run but the Plan has no `restart` change (it was stopped at plan time), the step fails with "the guest runs now; plan again". A stopped guest is not started.
- A restart that fails half way (review S5): the Run observes the guest after the error. The step fails with a text that names the power state, for example "the restart failed: the guest is stopped. Start it in Proxmox, then plan again." or "… the guest still runs with the old value …". No retry, no second power action, the Target is `failed` like after every failed step. Plumb has no desired power state, so nothing reports a stopped guest later; this text is the only signal.
- If the value is still pending after a restart that worked, the step fails ("still pending after the restart").
- After a restart the old link of the agent can still look online. The resource steps of the same Run wait for a new `hello` of the agent, as after a create and a re-enrollment (`newAgent` in `run.go`), within the same wait time.

### Steps
1. `internal/driver/driver.go`, `diff.go`: `GuestState.Pending`; `DriftGuest` with the rule above (`pending`, `also_pending`); `DiffGuest` gives a pending-only change of a mutable attribute the risk `reversible`. A pending-only change of a create-only attribute stays `destructive` drift and a `*CreateOnlyError` in `Diff`.
2. `internal/wire/wire.go`: `Change.Pending`, `Change.AlsoPending`. `api/openapi.yaml`: both in the non-sensitive `Change` branch. Mock: the same fields.
3. `internal/driver/proxmox/driver.go`: `Observe` reads `config?current=1` and `config`. Both answers must have the same digest; if not, read again (3 tries, then an error). `Pending = specOf(next)` when it differs from `specOf(running)`. `Update` keeps writing the desired value (see host check 2).
4. `internal/driver/fake/fake.go`: model "running" and "pending" for `cores` and `memory` of a running guest; `Power restart` applies pending values; a test helper sets a pending value from outside.
5. **[R]** `internal/plan/propose.go`: add the `restart` change by the rule above (the observed `Power` is known there).
6. **[R]** `internal/plan/run.go`: after `Update`, the restart rule above: one restart also with a re-enrollment, the observe after a failed restart, the wait for a new `hello`. A Go test pins `ShutdownTimeout` to 120 s.
7. `internal/plan/guest.go`: no rule change; the digest already moves with a pending change (fact 1). Check it with a test.
8. `internal/issue/issue.go`: accept uses `from` as today; add a test for a pending change.
9. Web: `ChangeTable.svelte` and `view.ts` show the tag and the lines (pending, also pending, create-only input); **[R]** the text of the `restart` change on `guest:config`; component tests; `browser-shot` light and dark for the review.

### Tests
- `internal/driver` (`diff_test.go`): the table of running / pending / desired for a mutable and a create-only attribute, with the third-value case (`also_pending`); risks.
- `internal/driver/proxmox` with the fake PVE. The fake must model: `POST config` on a running VM puts `cores`, `memory` and `smbios1` into pending and applies `net0`; `GET config` with and without `current`; a write of the running value removes the pending entry; stop + start applies pending values; the digest moves on each of these.
  - Outside pending `memory` → `Observe` has `Pending`, `Spec` is the running value.
  - `Update` back to the desired value → `Pending` is nil, no power call.
  - Digest differs between the two reads → retry, then an error.
- `internal/plan` with `driver/fake`:
  - Outside pending change → `CheckGuests` reports `differs` with `pending: true` in one round.
  - Revert Plan: risk `reversible`, no `restart` change, the Run sends no power action, the next round reports `unchanged`.
  - Accept: the Target override has the pending value. **[R]** The Plan has `restart`; the Run restarts; nothing is pending after it.
  - **[R]** Plumb's own update of `memory` on a running guest: the Plan has `restart`, the Run restarts, `Observe` shows the new running value. On a stopped guest: no restart and no start.
  - **[R]** A value that is still pending after the restart fails the step.
  - **[R]** Re-enrollment and a `memory` change in one Plan → one `Update`, one power action.
  - **[R]** The fake applies the change live (hotplug) → the planned restart is skipped, the step is `applied`.
  - **[R]** The start fails after the shutdown → the step fails, the text has "stopped", no second power action, the Target is `failed`, no resource step runs. The shutdown fails → the text says that the guest still runs.
  - **[R]** After the restart the resource steps wait for a new `hello`; an agent that only looks online from the old link gets no command.
  - **[R]** The guest runs at the Run and the Plan has no `restart` change → the step fails, no power action.
- Real host (in the T-015 acceptance run):
  1. `GET config` and `GET config?current=1` after `qm set --memory` on a running VM: values and digest as in fact 1.
  2. A write of the running value removes the pending entry (`qm pending` shows none). If not: `Update` sends `revert=<key>` for a pending-only change, and `checkUpdateParams` allows `revert` only with `cores`, `memory`, `net0`.
  3. The first form of the test step (no `qm reboot` after `qm set --memory`): the Issue with `pending` opens within 90 s; revert closes it; `qm pending` shows nothing; the boot ID is the same.
  4. **[R]** A Plan that changes `memory`: the guest gets a new boot ID, `free -m` shows the new size, and the resource steps of the same Run pass.

## Acceptance criteria
- [ ] A pending change of `cores` or `memory` from outside opens an Issue within one poll round (60 s), marked pending.
- [ ] Revert removes it without a restart, with the risk `reversible`.
- [ ] **[R]** Accept and Plumb's own update end with the value active. After every successful Run on a running guest, `Observe` shows no pending managed value.
- [ ] **[R]** A Plan that can restart a guest says so before the approval, with the hard power-off after 2 minutes.
- [ ] **[R]** A restart that fails leaves a failed step that names the power state of the guest.
- [ ] Tests: `make lint test build` passes; `make test-pve` passes on the test host with host checks 1 to 3, and 4 with **[R]**.

## Out of scope
- Hot-plug of memory or cores (hotplug and NUMA settings).
- Revert of a pending cloud-init input (for example `ipconfig0`): the Issue shows it as pending, a person removes it on the host (D-185 A: the driver writes no cloud-init input).
- LXC guests (T-038).

## Decisions
`docs/decisions/T-056.md` (D-190, D-191; D-191 changes one risk of D-063).

**User question (one, with the user now):** may a Run restart a running VM when the Plan changes `cores` or `memory`? The restart is a clean shutdown; if the VM is not off after 2 minutes, it is powered off hard; then it starts again. The Plan shows this before the approval. Recommendation: yes.
- **Yes:** the card is done as written. Nothing else changes.
- **No:** the **[R]** parts are dropped and the card comes back to pl-architect. Then "the value waits" needs its own state in Issue, Target view and API, and a way to restart from Plumb or a text that tells the user to do it.
- The parts without **[R]** (observe, drift, `pending` text, revert) do not depend on the answer. Without **[R]**, Plumb's own update of a running guest still says "applied" while the value waits (fact 3); the pending drift check then at least shows it in the next poll round.

## Result (owner, max 8 lines)
branch · commit · tests · notes

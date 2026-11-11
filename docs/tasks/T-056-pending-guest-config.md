# T-056 – Pending Proxmox changes: seen at once; a restart only where the Target allows it

- **Owner:** tbd (proposal: pl-backend for the contract and the backend; pl-frontend for the web part after the contract is merged)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M backend + S web (Claude)   · **Depends on:** T-012 and T-053 on main; migration 0007 (0006 is T-055)
- **State:** planned again for D-193 (2026-11-11). **Every part can start now.** The parts marked **[N]** use the proposed name and default of the setting (`allow_restart`, `false`). Another answer of the user changes one schema key, one default and the texts, no rule.

## Goal
1. A config change that waits in Proxmox for the next cold start is seen at once. A waiting value from outside is drift, and the Issue says that it is pending.
2. A Run restarts a running VM only when its Target allows it (D-193). When it does not, the new value waits, and Plumb shows "restart needed" as a state of the Target until the value is active.
3. A Run never says "applied" for a value that is not active without saying that it waits.

## Context (read only these)
- `docs/tasks/T-015.log.md` on `origin/pl-backend/T-015-real-host` – "Real-host run 1" and "Test change after run 1".
- `internal/driver/proxmox/driver.go` – `config` (`current=1`), `specOf`, `Observe`, `Update`, `checkUpdateParams`.
- `internal/driver/driver.go`, `diff.go` – `GuestState`, `DriftGuest`, `DiffGuest`, `guestRisk`.
- `internal/plan/guest.go` (`CheckGuests`, `Live`), `internal/plan/run.go` (`guestStep`), `internal/plan/propose.go` (`planRendered`).
- `internal/state/schema/target.json`, `internal/state/render.go` (`Rendered`), `internal/store` (`Binding`), `internal/api/handlers.go` (`targetStatus`, phase).
- `internal/wire/wire.go` (`Change`, `AttrRestart`), `api/openapi.yaml`, `internal/issue/issue.go` (accept).
- `web/src/ui/ChangeTable.svelte`, `web/src/screens/TargetForm.svelte`, `web/src/lib/view.ts`, `web/src/lib/mock/mock.ts`.
- D-038, D-063, D-078, D-185, D-193. New: D-190, D-191, D-194, D-195 (`docs/decisions/T-056.md`).

## Facts
1. Proven on the host (T-015 run 1): `memory` is not in the default `hotplug` list of Proxmox (`network,disk,usb`). `qm set --memory` on a running VM is stored as pending. `GET …/config?current=1` gives the running values; without `current` it gives the values after the next cold start. The digest covers both.
2. The driver reads only `current=1`. A pending change from outside is not seen until a cold start.
3. **From the code, not proven on the host:** the same is true for Plumb's own change. `Update` writes `cores` or `memory`; on a running VM that is pending. The Run does not restart the guest (only a re-enrollment does) and ends "applied". The Issue of a revert is then "resolved" while the VM still runs with the old value. In T-015 run 2 the revert of `memory` passed in 0.7 s this way.
4. From the code: `CheckGuests` compares only when the digest moved. The Target view has no place for "a value waits" today.

## Plan (architect)

### A. The setting **[N]** (D-194)
- **Name and place:** `spec.allow_restart` of a Target, a boolean, next to `guest`, `blocks` and `overrides`. UI label: "Plumb may restart this VM".
- **Not in `spec.guest`:** every key of `guest` is an attribute of `guest:config` that the driver compares with Proxmox, that a Change names and that `overrides` (Accept) can replace. This setting is a rule of Plumb. It is never written to Proxmox, it is never drift and it has no override.
- **Default `false`.** Reasons: (1) the two wrong defaults cost different things: a value that waits is visible and one restart fixes it; a VM that went down cannot be undone. (2) It is the behaviour of today (no Run restarts for `cores` or `memory`), so no existing Target changes. (3) It matches the user's reason for D-193 ("must not go down without the user").
- **Changeable.** A change is a normal proposal: new revision, Plan, approval. It is no guest change and has no risk of its own. It is not human-only: the restart itself stays a `disruptive` change in the same or a later Plan, so a token never restarts a VM above its `max_risk`.
- **Meaning:** `false` – no Run stops or restarts this VM while it runs. `true` – a Run may restart it; the Plan shows the restart before the approval.
- **Re-enrollment** needs a restart (D-038). On a running VM with `allow_restart: false` the proposal is refused: "Re-enrollment restarts the VM, and this Target does not allow a restart by a Run. Set `allow_restart` in the same proposal, or stop the VM first." The token is valid for 15 minutes only, so it cannot wait for the user. A stopped VM is re-enrolled as today.
- **"VM type":** Plumb has no document for a type of VM yet. The field is per Target. When Target templates exist (ARCHITECTURE §10), the template gives the default. The field is valid for `type: lxc` too; it has no effect there until an LXC change needs a restart (T-038).
- `state.Rendered` gets `AllowRestart bool`.

### B. Three views of one value (D-190, D-195)
`Observe` reads both views. `GuestState.Spec` is the running view, as today. The new `GuestState.Pending *GuestSpec` is the view after the next stop and start; it is nil when both are equal. Per attribute (R = running, P = after the next cold start, D = desired):

| R | P | Name | What it is |
|---|---|---|---|
| = D | = D | match | nothing |
| = D | ≠ D | pending drift | drift: `{from: P, to: D, pending: true}`. Someone set P outside Plumb. Issue. |
| ≠ D | = D | **waits** | no drift and no Issue. The desired value is set and needs a cold start. State of the Target (part D). |
| ≠ D | ≠ D | drift | drift: `{from: R, to: D}`, plus `also_pending: P` when P ≠ R. Issue. |

- `driver.DriftGuest` (the one rule of all drivers, D-063) reports the rows "pending drift" and "drift". The new pure `driver.WaitingGuest(desired, observed)` reports the row "waits" as `{from: R, to: D, waits_for_restart: true}`. Who wrote the waiting value does not matter (a Run of Plumb or a person with `qm set`).
- `wire.Change` gets three optional fields. `pending` (bool): `from` is set in the hypervisor but not active. `also_pending` (a value): `from` is the running value and this other value waits. `waits_for_restart` (bool): `to` is or will be set in the hypervisor and becomes active at the next stop and start. All three are for display; no risk rule reads them.
- Text for a pending change (web, Issue and Plan): tag `pending`, and "Set in Proxmox but not active. The VM uses <to>. <from> becomes active at the next stop and start of the VM. A reboot inside the guest is not enough." With `also_pending`: "Also pending: <value>."
- A pending create-only input (for example `ipconfig0`) is shown the same way, with: "Plumb does not revert this value. Remove it in Proxmox: use Revert on the pending value, or `qm set <vmid> --revert <key>`."
- **Revert** of a pending drift of `cores`, `memory` or `bridge` removes the pending value by a write of D. The running guest does not change: no restart (D-191).
- **Accept** works as today: `from` becomes the desired value. For a pending drift that is P. After the accept the row is "waits" (R ≠ D, P = D): the Plan has nothing to write. With `allow_restart: true` on a running guest it has the `restart` change.

### C. Plan and Run rule (D-194)
One pure function (proposal: `restartRule` in `internal/plan`, used by `planRendered` and `guestStep`) takes the changes of `DiffGuest`, the list of `WaitingGuest`, the observed power, `allow_restart` and "re-enrollment". `driver.ColdStart = {cores, memory}` names the attributes that wait on a running guest. `bridge` applies live and is not part of the rule.

| Guest | `allow_restart` | Result |
|---|---|---|
| stopped | any | The changes as they are. No restart, and the guest is not started. |
| running, Plan re-enrolls | `true` | The changes as they are. The restart of the re-enrollment makes them active: one `Update`, one restart. |
| running | `true` | If a "drift" change of `cores` or `memory` exists, or a value waits: add the `restart` change (`wire.AttrRestart`) on `guest:config`. A Plan can hold the `restart` change alone. |
| running | `false` | Each "drift" change of `cores` or `memory` gets `waits_for_restart: true`. Waiting values give no change. |

**Run with `allow_restart: true`** (the former [R] part, same rules):
- After `Update` the Run observes again. If a managed value still waits, it restarts the guest with `Power restart` (D-078) and observes once more. If nothing waits (hotplug), it skips the planned restart; that is no error.
- What the restart is: a clean shutdown; if the guest is not off after 120 s (`ShutdownTimeout`), Proxmox powers it off hard (`forceStop`); then a start. A Go test pins the 120 s, because the Plan text names it. Plan text: "Restart of the VM if Proxmox cannot apply the change live: clean shutdown, hard power-off after 2 minutes, then start."
- The guest runs at the Run, the rule wants a restart, and the stored Plan has no `restart` change (the guest was stopped at plan time): the step fails before any write with "the guest runs now; plan again".
- A restart that fails half way: the Run observes the guest after the error. The step fails with a text that names the power state ("the restart failed: the guest is stopped. Start it in Proxmox, then plan again." or "… the guest still runs with the old value …"). No retry, no second power action.
- A value that still waits after a restart that worked fails the step.
- After a restart the resource steps of the same Run wait for a new `hello` of the agent (`newAgent` in `run.go`).

**Run with `allow_restart: false`:**
- No power action, in no case.
- After `Update` the Run observes again. For each written `cores` or `memory`: R = D → the recorded change has no flag (the guest was stopped, or Proxmox applied it live). P = D → the recorded change has `waits_for_restart: true`. Neither → the step fails ("Proxmox did not take the value").
- The step status is `applied` (D-045 gets no new status): the value is written, and the change says that it waits. Step output: "<attr>: set in Proxmox, waits for a stop and start of the VM".
- The agent link does not change, so the resource steps run as today.
- Plan text of such a change: tag `after restart`, and "Plumb writes <to> to Proxmox. The VM keeps <from> until its next stop and start. This Run does not restart the VM (`allow_restart` is off). A reboot inside the guest is not enough."
- A revert Issue whose Run ends this way is `resolved` (D-068, no change): the outside value is gone from the config. The Target then shows "restart needed". An Issue that stays open until a restart would need a new Issue state; that is out of scope.

### D. The state "restart needed" (D-195)
- **A state, not an Issue.** Nobody changed anything against the desired state, so there is nothing to accept or revert. The poll reports `unchanged` for `guest:config` in this state.
- **Data:** `binding` gets the column `waiting` (JSON list of changes, default `[]`; migration 0007). It is written together with the digest: (a) in `CheckGuests` when the digest moves, (b) at the end of every Run for each of its Targets that has a guest. (b) is needed because Accept can change D without a write to Proxmox. The value is `WaitingGuest(desired, observed)`.
- **API:** `TargetStatus.waiting` (list of Change, always present) and the new phase `restart_needed`. Order of the phases: … `error`, `drifted`, `restart_needed`, `ready`. When the list changes, the engine emits one event for `target:<name>` (name by the convention of `internal/event`), so the UI updates and the history shows when the value became active.
- **Web:** badge "restart needed" in the Target list. On the Target page: "Restart needed. <attr>: the VM uses <from>. <to> is set and becomes active at the next stop and start of the VM. Stop and start the VM in Proxmox; a reboot inside the guest is not enough. Or switch on 'Plumb may restart this VM' and make a Plan."
- **What ends it:**
  1. The user stops and starts the VM outside Plumb. The digest moves, the next poll round (60 s) finds R = D, the list is empty. No Plan, no Issue.
  2. A Plan after `allow_restart` became `true` (the same proposal can do both): it holds the `restart` change, and the Run restarts.
  3. The desired value goes back to the running value: the row is "pending drift", the Plan removes the waiting value, no restart.
  4. Someone removes or changes the waiting value outside Plumb: the row is "drift", an Issue opens.

### E. Risk classes (D-195, changes D-063 for `cores` and `memory`)
| Change | Risk |
|---|---|
| `cores`, `memory`: every write (guest stopped; waits; removes a pending value) | `reversible` – the running guest does not change |
| `restart` on `guest:config` | `disruptive` – it carries the risk of a `cores` or `memory` change on a running guest that may restart |
| `bridge` | `disruptive` (no change: the network drops for a moment); `reversible` when the change only removes a pending value (D-191) |
| `enroll_token` (re-enrollment) | `disruptive` (no change) |
| a change of `allow_restart` | none of its own; not human-only |

So a Plan with `cores` or `memory` on a running guest is `disruptive` with `allow_restart: true` (as today) and `reversible` with `false`. A token with `max_risk: reversible` can set a value that waits and can revert a pending drift; it can never cause a restart. In the `false` case the risk does not move when the power state changes between Plan and Run, so no step is refused for that.

### Steps
Backend PR (contract first, so the web part can start against the mock):
1. `internal/wire/wire.go`: `Change.Pending`, `Change.AlsoPending`, `Change.WaitsForRestart`. `api/openapi.yaml`: the three fields in the non-sensitive `Change` branch; `TargetStatus.waiting`; phase `restart_needed`. `internal/state/schema/target.json`: **[N]** `spec.allow_restart` (boolean, default `false`, with a description). `npm run gen`; mock data with one Target in each state.
2. `internal/driver/driver.go`, `diff.go`: `GuestState.Pending`; `DriftGuest` by table B; `WaitingGuest`; `ColdStart`; `guestRisk` by table E. A pending-only change of a create-only attribute stays `destructive` drift and a `*CreateOnlyError` in `Diff`.
3. `internal/driver/proxmox/driver.go`: `Observe` reads `config?current=1` and `config`. Both answers must have the same digest; if not, read again (3 tries, then an error). `Pending = specOf(next)` when it differs from `specOf(running)`. `Update` keeps writing the desired value (host check 2).
4. `internal/driver/fake/fake.go`: model "running" and "pending" for `cores` and `memory` of a running guest; `Power restart` and stop + start apply pending values; test helpers set a pending value from outside and switch "applies live" (hotplug).
5. `internal/state`: **[N]** `Rendered.AllowRestart` from the Target.
6. `internal/plan`: `restartRule` (table C); `planRendered` uses it and refuses a re-enrollment by part A; `guestStep` by part C. `Update` gets only the changes of `DiffGuest`, never the `restart` change.
7. `internal/store` + migration 0007: `binding.waiting`. `internal/plan/guest.go`: write it by part D, emit the event. `internal/api`: `waiting` and the phase.
8. `internal/issue/issue.go`: accept uses `from` as today; add a test for a pending drift.

Web PR (after step 1 is on main):
9. `ChangeTable.svelte`, `view.ts`: tags and texts for `pending`, `also_pending`, `waits_for_restart`, the create-only input and the `restart` change on `guest:config`.
10. **[N]** `TargetForm.svelte`: the checkbox "Plumb may restart this VM", with the help text "Off: a change of cores or memory waits until you stop and start the VM. On: the Plan shows the restart, and the Run does it."
11. Target list and Target page: badge and text of part D. Component tests; `browser-shot` light and dark for the review.

### Tests
- `internal/driver` (`diff_test.go`): the table B for a mutable and a create-only attribute, with `also_pending`; `WaitingGuest`; the risks of table E.
- `internal/driver/proxmox` with the fake PVE. The fake must model: `POST config` on a running VM puts `cores`, `memory` and `smbios1` into pending and applies `net0`; `GET config` with and without `current`; a write of the running value removes the pending entry; stop + start applies pending values; the digest moves on each of these.
  - Outside pending `memory` → `Observe` has `Pending`, `Spec` is the running value.
  - `Update` back to the desired value → `Pending` is nil, no power call.
  - Digest differs between the two reads → retry, then an error.
- `internal/plan` (`restartRule`): the table C, one case per row, plus "waits + `true`" (restart alone) and "waits + `false`" (no change).
- `internal/plan` with `driver/fake`:
  - Outside pending change → `CheckGuests` reports `differs` with `pending: true` in one round. Revert Plan: risk `reversible`, no `restart` change, no power action, the next round reports `unchanged`.
  - **`false`:** `memory` change on a running guest → the Plan change has `waits_for_restart`, risk `reversible`; the Run sends no power action; the step is `applied` with the flag; `binding.waiting` has the value; the phase is `restart_needed`; two poll rounds open no Issue. Then stop + start in the fake → the next round empties the list, one event, no Issue.
  - **`false`:** the fake applies live → the recorded change has no flag, the list is empty. A stopped guest → the same, and the guest stays stopped.
  - **`false`:** the value is neither running nor pending after `Update` → the step fails.
  - **`false`:** re-enrollment of a running guest → the proposal is refused with the text of part A; with `allow_restart: true` in the same proposal → the Plan is made. Re-enrollment of a stopped guest → as today.
  - **`false`:** accept of a pending drift → the Run writes nothing, the Issue is resolved, the list has the value (write point (b) of part D).
  - **`false` → `true`** while a value waits → the Plan holds the `restart` change alone (action `restart`, `disruptive`); the Run restarts; the list is empty.
  - **`true`:** `memory` change on a running guest → `restart` change, one restart, `Observe` shows the new running value. On a stopped guest: no restart and no start.
  - **`true`:** re-enrollment and a `memory` change in one Plan → one `Update`, one power action.
  - **`true`:** the fake applies live → the planned restart is skipped, the step is `applied`.
  - **`true`:** the start fails after the shutdown → the step fails, the text has "stopped", no second power action, the Target is `failed`, no resource step runs. The shutdown fails → the text says that the guest still runs.
  - **`true`:** after the restart the resource steps wait for a new `hello`; an agent that only looks online from the old link gets no command.
  - **`true`:** the guest runs at the Run and the Plan has no `restart` change → the step fails before a write, no power action.
  - **`true`:** a value that still waits after the restart fails the step.
- `internal/state`: `allow_restart` validates, defaults to `false`, and is refused inside `guest` and `overrides`.
- `internal/store`: migration 0007 on a database with bindings; `waiting` round trip.
- Real host (in the T-015 acceptance run):
  1. `GET config` and `GET config?current=1` after `qm set --memory` on a running VM: values and digest as in fact 1.
  2. A write of the running value removes the pending entry (`qm pending` shows none). If not: `Update` sends `revert=<key>` for a pending drift, and `checkUpdateParams` allows `revert` only with `cores`, `memory`, `net0`.
  3. `qm set --memory` without a reboot: the Issue with `pending` opens within 90 s; revert closes it; `qm pending` shows nothing; the boot ID is the same.
  4. `allow_restart: false`, a Plan that changes `memory`: the Run succeeds, the boot ID is the same, `qm pending` shows the value, the API shows `waiting` and `restart_needed`, no Issue within 130 s. A `reboot` inside the guest does not end the state. Then `qm shutdown` + `qm start` on the host: within 90 s the list is empty, `free -m` shows the new size, no Issue. This also proves that the digest moves when a pending value becomes active (part D relies on it; if it does not, `CheckGuests` must also compare while the list is not empty).
  5. `allow_restart: true`, a Plan that changes `memory`: new boot ID, `free -m` shows the new size, the resource steps of the same Run pass.

## Acceptance criteria
- [ ] A pending change of `cores` or `memory` from outside opens an Issue within one poll round (60 s), marked pending. Revert removes it without a restart, with the risk `reversible`.
- [ ] **[N]** A Target has `allow_restart` (default `false`). With `false`, no Run sends a power action to a running guest, and a re-enrollment of a running guest is refused with a clear text.
- [ ] With `false`, a change of `cores` or `memory` on a running guest ends `applied` and marked as waiting; the Plan said so before the approval; the Target shows `restart_needed` with the values; no Issue opens for it.
- [ ] The state ends within 90 s after a stop and start of the VM outside Plumb, and with the Run of a Plan after `allow_restart` became `true`.
- [ ] With `true`, a Plan that can restart a guest says so before the approval, with the hard power-off after 2 minutes. After every successful Run nothing waits. A restart that fails leaves a failed step that names the power state of the guest.
- [ ] Tests: `make lint test build` passes; `make test-pve` passes on the test host with host checks 1 to 5.

## Out of scope
- Hot-plug of memory or cores (hotplug and NUMA settings).
- A "restart now" action in Plumb for a Target with `allow_restart: false` (today: restart in Proxmox, or switch the setting on for one Plan).
- An Issue that stays open until the value is active. A restart window or a schedule.
- Revert of a pending cloud-init input (for example `ipconfig0`): the Issue shows it as pending, a person removes it on the host (D-185 A).
- LXC guests (T-038). A default per VM type (needs Target templates).

## Decisions
`docs/decisions/T-056.md`: D-190 (two views, pending drift), D-191 (revert of a pending value; cut down to this), D-194 (the setting), D-195 (the state "restart needed" and the risks; changes D-063 for `cores` and `memory`).

**Questions for the user** (the card starts with the proposals; an answer changes names and one default only):
1. **Name:** `allow_restart` on the Target, label "Plumb may restart this VM". Alternative: `restart: plan | manual`.
2. **Default:** `false` (a value waits; nothing goes down by itself). Alternative: `true` (a change is active after the Run; the Plan shows the restart).
3. **Re-enrollment:** with `false` it is refused for a running VM, because it restarts the VM. Alternative: re-enrollment always restarts, also with `false`.

## Result (owner, max 8 lines)
branch · commit · tests · notes

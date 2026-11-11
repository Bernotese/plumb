# T-056 – Pending Proxmox changes: seen at once; a restart only where the Target allows it

- **Owner:** tbd (proposal: pl-backend for the contract and the backend; pl-frontend for the web part after the contract is merged)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M backend + S web (Claude)   · **Depends on:** T-012 and T-053 on main; migration 0007 (0006 is T-055)
- **State:** planned again for D-193 (2026-11-11), with the review of pl-reviewer on PR #94 worked in (B1, S1 … S8, N1 … N4). **Every part can start now.** The parts marked **[N]** use the proposals that are with the user (name `allow_restart`, default `false`, re-enrollment refused with `false`). Another answer changes one schema key, one default, the texts and, for the third question, the re-enrollment rule of part A.

## Goal
1. A config change that waits in Proxmox for the next cold start is seen at once. A waiting value from outside is drift, and the Issue says that it is pending.
2. A Run restarts a running VM only when its Target allows it (D-193). When it does not, the new value waits, and Plumb shows "restart needed" as a state of the Target until the value is active.
3. A Run, an Issue and the API never say "done" for a value that is not active without saying that it waits.

## Context (read only these)
- `docs/tasks/T-015.log.md` on `origin/pl-backend/T-015-real-host` – "Real-host run 1" and "Test change after run 1".
- `internal/driver/proxmox/driver.go` – `config` (`current=1`), `specOf`, `Observe`, `Update`, `checkUpdateParams`.
- `internal/driver/driver.go`, `diff.go` – `GuestState`, `DriftGuest`, `DiffGuest`, `guestRisk`.
- `internal/plan/guest.go` (`CheckGuests`, `Live`), `internal/plan/run.go` (`guestStep`), `internal/plan/propose.go` (`planRendered`).
- `internal/state/schema/target.json`, `internal/state/render.go` (`Rendered`), `internal/store` (`Binding`, `Run`), `internal/api/handlers.go` (`targetStatus`, phase, `reenroll`).
- `internal/wire/wire.go` (`Change`, `AttrRestart`), `api/openapi.yaml`, `internal/issue/issue.go` (accept, `gone`, comments).
- `web/src/ui/ChangeTable.svelte`, `web/src/screens/TargetForm.svelte`, `web/src/lib/view.ts`, `web/src/lib/mock/mock.ts`.
- D-038, D-063, D-068, D-078, D-185, D-193. New: D-190, D-191, D-194, D-195 (`docs/decisions/T-056.md`).

## Facts
1. Proven on the host (T-015 run 1): `memory` is not in the default `hotplug` list of Proxmox (`network,disk,usb`). `qm set --memory` on a running VM is stored as pending. `GET …/config?current=1` gives the running values; without `current` it gives the values after the next cold start. The digest covers both.
2. The driver reads only `current=1`. A pending change from outside is not seen until a cold start.
3. **From the code, not proven on the host:** the same is true for Plumb's own change. `Update` writes `cores` or `memory`; on a running VM that is pending. The Run does not restart the guest (only a re-enrollment does) and ends "applied". The Issue of a revert is then "resolved" while the VM still runs with the old value. In T-015 run 2 the revert of `memory` passed in 0.7 s this way.
4. From the code: `CheckGuests` compares only when the digest moved. The stored digest already covers pending values. `POST /targets/{name}/reenroll` takes only a reason. The Target view has no place for "a value waits" today.

## Plan (architect)

### A. The setting **[N]** (D-194)
- **Name and place:** `spec.allow_restart` of a Target, a boolean, next to `guest`, `blocks` and `overrides`. UI label: "Plumb may restart this VM".
- **Not in `spec.guest`:** every key of `guest` is an attribute of `guest:config` that the driver compares with Proxmox and that `overrides` (Accept) can replace. This setting is a rule of Plumb. It is never written to Proxmox, it is never drift and it has no override.
- **Default `false`.** Reasons: (1) the two wrong defaults cost different things: a value that waits is visible and one restart fixes it; a VM that went down cannot be undone. (2) It is the behaviour of today (no Run restarts for `cores` or `memory`), so no existing Target changes. (3) It matches the user's reason for D-193 ("must not go down without the user").
- **Changeable, and always visible (review S2).** A change is a normal proposal: new revision, Plan, approval. The Plan shows it as the change `allow_restart: <from> → <to>` with the risk `read` on `guest:config`. It is a Plan-only change like `enroll_token`: the driver never gets it, and the step records it with no call to Proxmox. A Plan that only changes the setting is not empty. It is not human-only: the restart itself stays a `disruptive` change in the same or a later Plan, so a token never restarts a VM above its `max_risk`.
- **Meaning:** `false` – no Run stops or restarts this VM while it runs. `true` – a Run may restart it; the Plan shows the restart, and why, before the approval.
- **Re-enrollment** needs a cold start (D-038). A re-enrollment starts a stopped VM, as today, with `true` and with `false`. With `false` these rules hold:
  1. *Proposal:* refused for a running VM with "Re-enrollment restarts the VM, and this Target does not allow a restart by a Run. Stop the VM in Proxmox and re-enroll again (the Run starts it). Or set `allow_restart` first (its own Plan), then re-enroll." Both ways exist in the API and the web today; the re-enroll endpoint gets no new field (review S1). With the second way the setting stays `true` until the user switches it back; the web dialog says so. The token lives 15 minutes, so it cannot wait for the user.
  2. *Run (review B1):* the power check runs again before the token is made and before any write. The guest runs → the step fails with "the guest runs now, and this Target does not allow a restart by a Run. Stop the VM or set `allow_restart`, then plan again." No token, no write, no power action.
  3. *Run, guest stopped:* the Run writes the token and sends `start`, never `restart`. So a VM that someone starts between the check and the power action is not restarted. If the token still waits after `start`, the step fails with "the VM was started outside Plumb during the Run; the new token is not active. Plan the re-enrollment again."
- **"VM type":** Plumb has no document for a type of VM yet. The field is per Target. When Target templates exist (ARCHITECTURE §10), the template gives the default. The field is valid for `type: lxc` too; it has no effect there until an LXC change needs a restart (T-038).
- `state.Rendered` gets `AllowRestart bool`.

### B. Three views of one value (D-190, D-195)
`Observe` reads both views. `GuestState.Spec` is the running view, as today. The new `GuestState.Pending *GuestSpec` is the view after the next stop and start; it is nil when both are equal. Per attribute (R = running, P = after the next cold start, D = desired):

| R | P | Name | What it is |
|---|---|---|---|
| = D | = D | match | nothing |
| = D | ≠ D | pending drift | drift: `{from: P, to: D, pending: true}`. Someone set P outside Plumb. Issue. |
| ≠ D | = D | **waits** (only `cores`, `memory`) | no drift. The desired value is set and needs a cold start. State of the Target (part D). |
| ≠ D | ≠ D | drift | drift: `{from: R, to: D}`, plus `also_pending: P` when P ≠ R. Issue. |

- **Scope of "waits" (review S4):** only `driver.ColdStart = {cores, memory}`. For every other attribute the row "R ≠ D, P = D" is the row "drift" (with `also_pending: P`). `bridge` (a hot-unplug that failed) is `disruptive` drift; no restart is planned for it; a `bridge` that still waits after `Update` fails the step ("Proxmox did not apply the bridge change live; check the VM in Proxmox"). A create-only input stays `destructive` drift.
- `driver.DriftGuest` (the one rule of all drivers, D-063) reports the rows "pending drift" and "drift". The new pure `driver.WaitingGuest(desired, observed)` reports the row "waits" as `{from: R, to: D, waits_for_restart: true}`. Who wrote the waiting value does not matter (a Run of Plumb or a person with `qm set`).
- `wire.Change` gets three optional fields. `pending` (bool): `from` is set in the hypervisor but not active. `also_pending` (a value): `from` is the running value and this other value waits. `waits_for_restart` (bool): `to` is or will be set in the hypervisor and becomes active at the next stop and start. All three are for display; no risk rule reads them.
- Text for a pending change (web, Issue and Plan): tag `pending`, and "Set in Proxmox but not active. The VM uses <to>. <from> becomes active at the next stop and start of the VM. A reboot inside the guest is not enough." With `also_pending`: "Also pending: <value>."
- A pending create-only input (for example `ipconfig0`) is shown the same way, with: "Plumb does not revert this value. Remove it in Proxmox: use Revert on the pending value, or `qm set <vmid> --revert <key>`."
- **Revert** of a pending drift of `cores`, `memory` or `bridge` removes the pending value by a write of D. The running guest does not change: no restart, risk `reversible` (D-191).
- **Accept** works as today: `from` becomes the desired value. For a pending drift that is P. After the accept the row is "waits" (R ≠ D, P = D): the Plan has nothing to write. With `allow_restart: true` on a running guest it has the `restart` change.

### C. Plan and Run rule (D-194)
One pure function (proposal: `restartRule` in `internal/plan`, used by `planRendered` and `guestStep`) takes the changes of `DiffGuest`, the list of `WaitingGuest`, the observed power, `allow_restart` and "re-enrollment". `guestStep` observes the power state before any write and uses the rule with that state.

| Guest | `allow_restart` | Result |
|---|---|---|
| stopped | any | The changes as they are. No restart. The guest is not started (only a re-enrollment starts it, part A). |
| running, Plan re-enrolls | `true` | The changes as they are. `Update` gets the changes and the token in one write; the one restart of the re-enrollment makes all of them active. |
| running, Plan re-enrolls | `false` | Refused (part A). |
| running | `true` | If a "drift" change of `cores` or `memory` exists, or a value waits: add the `restart` change (`wire.AttrRestart`) on `guest:config`. The values that already wait are listed as changes with `waits_for_restart: true` and the risk `read` (nothing is written for them), so the approver sees why the restart is there. |
| running | `false` | Each "drift" change of `cores` or `memory` gets `waits_for_restart: true`. Waiting values give no change. |

**Run with `allow_restart: true`** (the former [R] part, same rules):
- After `Update` the Run observes again. If a managed value still waits, it restarts the guest with `Power restart` (D-078) and observes once more. If nothing waits (hotplug), it skips the planned restart; that is no error.
- What the restart is: a clean shutdown; if the guest is not off after 120 s (`ShutdownTimeout`), Proxmox powers it off hard (`forceStop`); then a start. A Go test pins the 120 s, because the Plan text names it. Plan text: "Restart of the VM if Proxmox cannot apply the change live: clean shutdown, hard power-off after 2 minutes, then start."
- The guest runs at the Run, the rule wants a restart, and the stored Plan has no `restart` change (the guest was stopped at plan time): the step fails before any write with "the guest runs now; plan again".
- A restart that fails half way: the Run observes the guest after the error. The step fails with a text that names the power state ("the restart failed: the guest is stopped. Start it in Proxmox, then plan again." or "… the guest still runs with the old value …"). No retry, no second power action.
- A value that still waits after a restart that worked fails the step.
- After a restart the resource steps of the same Run wait for a new `hello` of the agent (`newAgent` in `run.go`).

**Run with `allow_restart: false`:**
- No `stop` and no `restart`, in no case. The only power action is the `start` of a re-enrollment on a stopped guest (part A).
- After `Update` the Run observes again. For each written `cores` or `memory`: R = D → the recorded change has no flag (the guest was stopped, or Proxmox applied it live). P = D → the recorded change has `waits_for_restart: true`. Neither → the step fails ("Proxmox did not take the value").
- The step status is `applied` (D-045 gets no new status): the value is written, and the change says that it waits. Step output: "<attr>: set in Proxmox, waits for a stop and start of the VM".
- The agent link does not change, so the resource steps run as today.
- Plan text of such a change: tag `after restart`, and "Plumb writes <to> to Proxmox. The VM keeps <from> until its next stop and start. That can be a restart that nobody plans (host maintenance, HA). This Run does not restart the VM (`allow_restart` is off). A reboot inside the guest is not enough."

**"Succeeded" must not hide a waiting value (review S5):**
- **Run:** the Run gets `waits_for_restart`: the number of changes that its steps recorded with the flag (stored at the end of the Run, migration 0007; in the API of a Run and of the Run list). The Run view shows "N changes wait for a restart of the VM". A client that reads only `status: succeeded` and this number knows that a value is not active.
- **Issue:** an Issue of `guest:config` that is resolved (`gone`, `revert` or `accept`) while the waiting list of its Target is not empty gets a comment of `system`: "Resolved, but not active: <attr> <to> is set and waits for a restart of the VM. The VM uses <from>." The `issue.resolved` event carries `waits_for_restart: <count>`. The Issue view shows the comment and links to the Target. For this the waiting list is written before the Issue is resolved (poll: before the drift report; Run: before the Issue hook).
- An Issue that stays open until a restart would need a new Issue state; that is out of scope.

### D. The state "restart needed" (D-195)
- **A state, not an Issue.** Nobody can accept or revert it: the desired value is already set. The poll reports `unchanged` for `guest:config` when nothing else differs.
- **Data (migration 0007):** `binding.waiting`, a JSON list of `{attr, from, to, since}`, default `[]`. `since` is the time when Plumb first saw this `attr` with this `to` in the list; it stays while both stay (review S6). The list is `WaitingGuest(desired, observed)` and is written together with the digest:
  - (a) in `CheckGuests` when the digest moved **or the stored list is not empty** (review S8: the end of the state must not depend on a digest that moves; `Observe` runs every round anyway);
  - (b) at the end of every Run, **succeeded or failed**, for each of its Targets that has a guest. Needed because the desired value moves with the approval, also when the Run fails, and because Accept can change D without a write to Proxmox.
- **Migration 0007 also sets `digest = ''` for all bindings (review S7).** The stored digests already cover pending values, so without this the first compare of a guest would wait for an unknown time. With it, the first poll round after the upgrade compares every guest once: a pending drift opens its Issue, and a value that a Run wrote before the upgrade enters the list (`since` = that round; the text says "since at least").
- **API:** `TargetStatus.waiting` (list, always present) and the new phase `restart_needed`. Order of the phases: … `error`, `drifted`, `restart_needed`, `ready`. The phase is one word for the list view; the list itself is the truth. The API description says that the phase and the list cannot occur for an LXC Target yet. When the list changes, the engine emits one event for `target:<name>` (name by the convention of `internal/event`), so the UI updates and the history shows when the value became active.
- **Web:** the badge "restart needed" and the text depend on `waiting`, not on the phase, so they show also when the phase is `drifted` or `error` (review S6). Target list: badge with the age. Target page: "Restart needed since <since>. <attr>: the VM uses <from>. <to> is set and becomes active at the next stop and start of the VM. Stop and start the VM in Proxmox; a reboot inside the guest is not enough. Or switch on 'Plumb may restart this VM' and make a Plan."
- **What ends it:**
  1. The user stops and starts the VM outside Plumb. The next poll round (60 s) finds R = D, the list is empty. No Plan, no Issue.
  2. A Plan after `allow_restart` became `true` (the same proposal can do both): it holds the waiting values and the `restart` change, and the Run restarts.
  3. The desired value goes back to the running value: the row is "pending drift", the Plan removes the waiting value, no restart.
  4. Someone removes or changes the waiting value outside Plumb: the row is "drift", an Issue opens.

### E. Risk classes (D-063 stays; D-191 is the only change)
| Change | Risk |
|---|---|
| `cores`, `memory`: a write of a new value (guest stopped, or the value will wait) | `disruptive` (D-063, no change) |
| `cores`, `memory`, `bridge`: a write that only removes a pending value (the result is the running value) | `reversible` (D-191) |
| `restart` on `guest:config` | `disruptive` |
| a value that already waits, listed next to a `restart` change | `read` (nothing is written) |
| `bridge` | `disruptive` (no change) |
| `enroll_token` (re-enrollment) | `disruptive` (no change) |
| `allow_restart` | `read`; not human-only |

**Why a write that waits is `disruptive` (review S3; the first form of this plan said `reversible`):** the write decides what the VM is after its next cold start, and that cold start can come with no Plan and no approver (host maintenance, HA recovery, `qm reboot`). A token with `max_risk: reversible` must not set `memory: 256` on a VM for that moment. So such a token can revert a pending drift, and it can change `allow_restart`; it cannot set a new size and cannot cause a restart. The risk of a `cores` or `memory` change does not depend on the power state, so no step is refused because the guest stopped or started between Plan and Run.

### Steps
Backend PR (contract first, so the web part can start against the mock):
1. `internal/wire/wire.go`: `Change.Pending`, `Change.AlsoPending`, `Change.WaitsForRestart`. `api/openapi.yaml`: the three fields in the non-sensitive `Change` branch; `TargetStatus.waiting` (`{attr, from, to, since}`); phase `restart_needed` (with the LXC note); `Run.waits_for_restart`. `internal/state/schema/target.json`: **[N]** `spec.allow_restart` (boolean, default `false`, with a description). `npm run gen`; mock data with one Target in each state.
2. `internal/driver/driver.go`, `diff.go`: `GuestState.Pending`; `DriftGuest` by table B; `WaitingGuest`; `ColdStart`; the risk of a pending-only removal (D-191). A pending-only change of a create-only attribute stays `destructive` drift and a `*CreateOnlyError` in `Diff`.
3. `internal/driver/proxmox/driver.go`: `Observe` reads `config?current=1` and `config`. Both answers must have the same digest; if not, read again (3 tries, then an error for this guest; the poll goes on with the next guest, as today). `Pending = specOf(next)` when it differs from `specOf(running)`. `Update` keeps writing the desired value (host check 2). **Fallback, if host check 1 shows that the two views do not give the same digest by design:** read `current=1`, then `config`, then `current=1` again, and accept when the first and the third answer have the same digest. `PowerStart` exists already; no new driver method.
4. `internal/driver/fake/fake.go`: model "running" and "pending" for `cores`, `memory` and the token of a running guest; `Power restart` and stop + start apply pending values, `start` on a running guest does nothing; test helpers set a pending value from outside, switch "applies live" (hotplug) and start a guest from outside.
5. `internal/state`: **[N]** `Rendered.AllowRestart` from the Target.
6. `internal/plan`: `restartRule` (table C); the Plan-only change `allow_restart` (from the render of the base revision and of the proposal); `planRendered` and `guestStep` by parts A and C. `Update` gets only the changes of `DiffGuest` (and the token), never `restart`, `allow_restart` or a `read` change.
7. `internal/store` + migration 0007: `binding.waiting`, `digest = ''` for all bindings, `run.waits_for_restart`. `internal/plan/guest.go`: the two write points of part D, `since`, the event. `internal/api`: `waiting`, the phase, the number of the Run.
8. `internal/issue/issue.go`: accept uses `from` as today; the `system` comment and the event field of part C.

Web PR (after step 1 is on main):
9. `ChangeTable.svelte`, `view.ts`: tags and texts for `pending`, `also_pending`, `waits_for_restart`, the create-only input, the `restart` change on `guest:config` and the `allow_restart` line.
10. **[N]** `TargetForm.svelte`: the checkbox "Plumb may restart this VM", with the help text "Off: a change of cores or memory waits until you stop and start the VM. On: the Plan shows the restart, and the Run does it." The re-enroll dialog shows the refusal text of part A.
11. Target list and Target page: badge, age and text of part D. Run view: "N changes wait for a restart". Component tests; `browser-shot` light and dark for the review.

### Tests
- `internal/driver` (`diff_test.go`): the table B for `cores`, `memory`, `bridge` and a create-only attribute, with `also_pending`; `WaitingGuest` lists only `cores` and `memory`; `bridge` and a create-only input with R ≠ D, P = D are drift; the risks of table E.
- `internal/driver/proxmox` with the fake PVE. The fake must model: `POST config` on a running VM puts `cores`, `memory` and `smbios1` into pending and applies `net0`; `GET config` with and without `current`; a write of the running value removes the pending entry; stop + start applies pending values; the digest moves on each of these.
  - Outside pending `memory` → `Observe` has `Pending`, `Spec` is the running value.
  - `Update` back to the desired value → `Pending` is nil, no power call.
  - Digest differs between the two reads → retry, then an error.
- `internal/plan` (`restartRule`): the table C, one case per row, plus "waits + `true`" (waiting values with `read`, and `restart`) and "waits + `false`" (no change).
- `internal/plan` with `driver/fake`:
  - Outside pending change → `CheckGuests` reports `differs` with `pending: true` in one round. Revert Plan: risk `reversible`, no `restart` change, no power action, the next round reports `unchanged`.
  - A proposal that only changes `allow_restart` → the Plan is not empty: one change `allow_restart` with the risk `read`; the Run calls no driver write; a token with `max_risk: reversible` can approve it.
  - **`false`:** `memory` change on a running guest → the Plan change has `waits_for_restart` and the risk `disruptive`; a token with `max_risk: reversible` cannot approve it; the Run sends no power action; the step is `applied` with the flag; the Run has `waits_for_restart: 1`; `binding.waiting` has the value with `since`; the phase is `restart_needed`; two poll rounds open no Issue and keep `since`. Then stop + start in the fake **with a digest that does not move** → the next round empties the list, one event, no Issue.
  - **`false`:** the fake applies live → the recorded change has no flag, the list is empty, the Run has `waits_for_restart: 0`. A stopped guest → the same, and the guest stays stopped.
  - **`false`:** the value is neither running nor pending after `Update` → the step fails. A `bridge` that still waits after `Update` → the step fails (also with `true`, and no restart).
  - **`false`, re-enrollment:** of a running guest → the proposal is refused with the text of part A. Of a stopped guest → the Run sends `start` only, and the guest enrolls. **Plan made while stopped, guest started before the Run → the step fails, no token is made, no write, no power action (B1).** Guest started between the check and the power action (fake hook) → `start` only, the step fails with the "started outside Plumb" text, the guest was not restarted.
  - **`false`:** accept of a pending drift → the Run writes nothing, the Issue is resolved with the `system` comment, the list has the value.
  - **`false`:** real drift (R ≠ D after a cold start), then a pending value = D from outside → the poll resolves the Issue as `gone` with the `system` comment and `waits_for_restart: 1` in the event; the list has the value. A revert Run that ends with a waiting value → the same comment.
  - **Failed Run:** a Run that fails after the approval moved D → the list is written for its Targets (write point (b)).
  - **Phase:** a Target with an open Issue and a waiting value → phase `drifted`, `waiting` not empty.
  - **`false` → `true`** while a value waits → the Plan holds `allow_restart` (`read`), the waiting value (`read`, flag) and `restart` (`disruptive`); the Run restarts; the list is empty.
  - **`true`:** `memory` change on a running guest → `restart` change, one restart, `Observe` shows the new running value. On a stopped guest: no restart and no start.
  - **`true`:** re-enrollment and a `memory` change in one Plan → one `Update` with the changes and the token, one power action.
  - **`true`:** the fake applies live → the planned restart is skipped, the step is `applied`.
  - **`true`:** the start fails after the shutdown → the step fails, the text has "stopped", no second power action, the Target is `failed`, no resource step runs. The shutdown fails → the text says that the guest still runs.
  - **`true`:** after the restart the resource steps wait for a new `hello`; an agent that only looks online from the old link gets no command.
  - **`true`:** the guest runs at the Run and the Plan has no `restart` change → the step fails before a write, no power action.
  - **`true`:** a value that still waits after the restart fails the step.
- `internal/state`: `allow_restart` validates, defaults to `false`, and is refused inside `guest` and `overrides`.
- `internal/store`: migration 0007 on a database with bindings: every digest is empty after it; a migrated binding whose guest has a pending value from outside is reported in poll round 1, and one with a waiting desired value enters the list in round 1; `waiting` round trip.
- Real host (in the T-015 acceptance run):
  1. `GET config` and `GET config?current=1` after `qm set --memory` on a running VM: values as in fact 1, and **the same digest in both answers** (if not: the fallback of step 3). Also look at `GET …/qemu/{vmid}/pending`: does it give the value and the pending value of every managed key in one read, and a digest? If yes, write it into the log as a simpler way for `Observe`.
  2. A write of the running value removes the pending entry (`qm pending` shows none). If not: `Update` sends `revert=<key>` for a pending drift, and `checkUpdateParams` allows `revert` only with `cores`, `memory`, `net0`.
  3. `qm set --memory` without a reboot: the Issue with `pending` opens within 90 s; revert closes it; `qm pending` shows nothing; the boot ID is the same.
  4. `allow_restart: false`, a Plan that changes `memory`: the Run succeeds with `waits_for_restart: 1`, the boot ID is the same, `qm pending` shows the value, the API shows `waiting` and `restart_needed`, no Issue within 130 s. A `reboot` inside the guest does not end the state. Then `qm shutdown` + `qm start` on the host: within 90 s the list is empty, `free -m` shows the new size, no Issue. Write into the log whether the digest moved (a fact only; the code does not rely on it).
  5. `allow_restart: true`, a Plan that changes `memory`: new boot ID, `free -m` shows the new size, the resource steps of the same Run pass.
  6. `allow_restart: false`, re-enrollment: refused while the VM runs; after `qm shutdown` the Run starts the VM and the agent enrolls.

## Acceptance criteria
- [ ] A pending change of `cores` or `memory` from outside opens an Issue within one poll round (60 s), marked pending. Revert removes it without a restart, with the risk `reversible`.
- [ ] **[N]** A Target has `allow_restart` (default `false`). A change of it is a visible Plan line. With `false`, no Run sends `stop` or `restart` to a guest, also when the guest was started after the Plan was made; a re-enrollment of a running guest is refused at the proposal and at the Run, with a text that names two real ways.
- [ ] With `false`, a change of `cores` or `memory` on a running guest ends `applied` and marked as waiting, with the risk `disruptive`; the Plan said so before the approval; the Run carries the number of waiting changes; the Target shows the values and their age, also when its phase is `drifted`; no Issue opens for it.
- [ ] An Issue that is resolved while a value waits says so in a `system` comment.
- [ ] The state ends within 90 s after a stop and start of the VM outside Plumb, also when the digest does not move, and with the Run of a Plan after `allow_restart` became `true`.
- [ ] After the upgrade, the first poll round compares every guest (migration 0007).
- [ ] With `true`, a Plan that can restart a guest says so before the approval, with the reason and the hard power-off after 2 minutes. After every successful Run nothing waits. A restart that fails leaves a failed step that names the power state of the guest.
- [ ] Tests: `make lint test build` passes; `make test-pve` passes on the test host with host checks 1 to 6.

## Out of scope
- Hot-plug of memory or cores (hotplug and NUMA settings).
- A "restart now" action in Plumb for a Target with `allow_restart: false` (today: restart in Proxmox, or switch the setting on for one Plan).
- An Issue that stays open until the value is active. A restart window or a schedule.
- Revert of a pending cloud-init input (for example `ipconfig0`): the Issue shows it as pending, a person removes it on the host (D-185 A).
- LXC guests (T-038). A default per VM type (needs Target templates).

## Decisions
`docs/decisions/T-056.md`: D-190 (two views, pending drift), D-191 (revert of a pending value; the one change of D-063), D-194 (the setting, the Plan and Run rule, re-enrollment), D-195 (the state "restart needed", where it is shown, the migration).

**Questions for the user** (with the user now; the card starts with the proposals):
1. **Name:** `allow_restart` on the Target, label "Plumb may restart this VM". Alternative: `restart: plan | manual`.
2. **Default:** `false` (a value waits; nothing goes down by itself). Alternative: `true` (a change is active after the Run; the Plan shows the restart).
3. **Re-enrollment:** with `false` it is refused for a running VM, because it restarts the VM. The user stops the VM first, or switches the setting on. Alternative: re-enrollment always restarts, also with `false`. Cost of the alternative: the setting is then a half promise ("no restart, except for a re-enrollment"), and a token with `max_risk: disruptive` can take a protected VM down through it.

## Result (owner, max 8 lines)
branch · commit · tests · notes

## User answers (pl-lead, 2026-11-11, D-196)
- The [N] parts are confirmed as proposed: `allow_restart` (yes/no, label "Plumb may restart this VM"), default no, re-enrollment of a running VM refused with no.
- Web part, extra item: a short hint at the address field of the Target form about the Ubuntu 26.04 limit (D-192): a stock Ubuntu 26.04 image takes a static address only from the second boot; use DHCP.

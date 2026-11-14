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

## Plan (architect)

> **USER points for pl-lead (D-326 (4); ask, never assume):**
> - **U7** – the user's own install gets the new ACL lines only by a repair run of the installer that **the user** starts. Ask when C2 is merged, with the exact `--dry-run` text of C2 for the user's flags. Until then the user's controller refuses every new NIC bridge/tag and every guest backup with a clear reason (nothing fails half way).
> - **U8** – which **extra bridges and VLAN tags** (`--extra-bridge`, `--extra-vlan`) and which **guest backup storage** (`--guest-backup-storage`) on the user's own host. If that storage is not a PBS with client-side encryption: does the user accept backups **in clear** there (`--guest-backup-clear`, D-406)? No code depends on the answer. Tell the user plainly (M3 of #321): with the D-380 rights, **the token itself** can back up, restore over (overwrite) and delete the backups of every pool guest directly at the Proxmox API; the human-only rule of Plumb holds only inside Plumb.
> - **Later (not slice 7):** a restore after the guest is gone would need `Datastore.Allocate` on the backup storage, a new right (B1 of #321). pl-lead tells the user; it is a later USER question (ROADMAP row 34).
> - Nothing else of slice 7 needs a right beyond D-379 / D-380 or touches the user's install. The only host network change is `vmbr9` on `pve-dev` (C8, D-414).

Inputs: draft `docs/plans/slice-7-draft.md` (`pl-architect/slice-7-draft`), host facts `docs/plans/host-facts-slice-7.md` (T-214, `pl-backend/T-214-host-facts-slice-7`) and `host-facts.md` §1, §3 (T-139), risks §6 (T-140), D-379, D-380, D-394 … D-397, `T-192.log.md`, review sweep of #272 … #320. Code: `main` after #318. Decisions: `docs/decisions/T-215.md` (D-398 … D-415).

### Scope (D-398)
**Part A – more NICs and VLAN tags** (ROADMAP row 14, D-379)
1. `guest.nics`: up to 3 more NICs, each `{bridge, tag?, ip?, gateway?}` (D-399); a MAC that Plumb chooses (D-400).
2. Rights exactly by D-379: one `SDN.Use` line per named bridge (untagged) or bridge/tag path (D-401); the server refuses an unnamed bridge or tag before PVE does.
3. Changes after create: add/remove/re-tag a NIC is `disruptive` (D-402). The guest-side IP of NIC N: VM by the agent's `network` resource matched by MAC, LXC by Proxmox `netN` (D-403).

**Part B – guest backups and restore** (ROADMAP row 15, D-380)
4. Target field `backup` (schedule, keep); backups are controller jobs, not Plans; "Back up now" (D-408).
5. Rights exactly by D-380 (D-405); the encryption state of the storage is read; a clear storage only with the install flag (D-406).
6. Identity: notes marker with a per-Target backup lineage (D-407).
7. Delete one backup: human admin session only (D-409). Restore **over the same bound guest** only: a Plan, human-only `guest-restore`, `destructive`; the credential is revoked and the agent re-enrolls (D-410). No restore after the guest is gone in slice 7 (D-411).

**Part C – slice 6 follow-ups** (D-415): the ROADMAP rows 28 … 33 below.

**Token rights:** exactly D-379 and D-380, nothing more (C2 shows each line). No `Datastore.Allocate`, no `Sys.Modify`, no propagate 1 on a bridge, no `prune-backups`, no `protected`.

### Contracts
**C1 (pl-architect: `api/openapi.yaml`, `web/src/lib/api.gen.ts`, `internal/state/schema/target.json`, ARCH §20 "Slice 7")**
- **Target schema** (no document migration): `guest.bridge`/`ip`/`gateway` stay NIC 0. New `guest.nics`: `maxItems 3`, item `{bridge (name pattern), tag? (1 … 4094), ip? ("dhcp" | CIDR), gateway? (IP)}`; at most one `gateway` over NIC 0 and `nics`; no `trunks`; `additionalProperties: false`. A NIC without `ip` is up without an address (no DHCP).
- **Target schema:** `backup?: {schedule: "daily" | "weekly", weekday? ("mon" … "sun", only with weekly), time ("HH:MM", UTC), keep (1 … 30)}`. The storage is never a document field (D-405).
- **422 `network-not-allowed`** at propose (and again at approval): a `nics[].bridge` or `bridge/tag` that the install has no ACL line for; the detail names the installer flag. **422 `nics-not-adoptable`**: `nics` on an adopted Target (D-404).
- **Backups:**
  - `GET /targets/{name}/backups` (read): `{storage: {id, type, encrypted: "pbs-key" | "stated-clear" | "clear-refused", free_pct}, items: [{volid, time, size, kind: "plumb" | "older-guest" | "unmarked", lineage_ok}], next_at, last: {at, result, message}}`;
  - `POST /targets/{name}/backups` (tier `apply`, rate `proposal`) "Back up now" → 202 with the job;
  - `DELETE /targets/{name}/backups/{volid}` (**human admin session only**, D-409) → 204;
  - `POST /targets/{name}/restore` (tier `plan`, rate `proposal`) `{volid, reason}` (over the same bound guest only, D-411) → 201 Plan (origin `restore`, risk `destructive`, human-only `guest-restore`).
- **Events:** `guest_backup.started|done|failed|deleted`.
- **Settings ACL view:** every new line with its state.
- **Hello / agent:** no new command type (the `network` resource only gains a match by MAC, C4).
- **Repair key view (ROADMAP row 32):** `RepairKeyView.left_behind` moves out of `previous` to the top level, so the parked and forgotten guests show also without a previous key; `previous.left_behind` stays one release as a copy.
- Every new operation in `roleHints.ts`, the tier test and the `x-plumb-rate` marks (A1 rule of slice 6).
- **MCP:** no new tool in slice 7 (the fixed set of D-364 stays; backups are not offered to a client).
- **ARCH §20** says in one sentence that `guest-restore` is human-only inside Plumb, but the D-380 rights let the token restore, back up and delete pool guests' backups at the PVE API directly (M3 of #321).

### Child cards (numbers from pl-lead)
Reviewer per D-394: the critical parts (token rights/ACLs, installer, secrets in backups, credential revoke and re-enroll) → **pl-reviewer**; others → a free agent, never the author.

| # | Title | Owner | Reviewer | Size | Depends on | Files it owns | CI job that proves it |
|---|---|---|---|---|---|---|---|
| C1 | Contract slice 7 (above) | pl-architect | pl-reviewer | M | – (**now**) | `api/openapi.yaml`, `web/src/lib/api.gen.ts`, `internal/state/schema/target.json`, `docs/ARCHITECTURE.md` §20 | `make lint test` (check:api, schema tests) |
| C2 | Installer + env + ACL view: `--extra-bridge <b>` (untagged), `--extra-vlan <b>:<T>[,<T>…]`, `--guest-backup-storage <id>`, `--guest-backup-clear`; roles `PlumbBridge` per path, new `PlumbGuestBackupVM` (`VM.Backup` on `/pool/plumb`, its own line, M1 of #321) and new `PlumbGuestBackup`; dry-run lists each line; a repair run removes lines no longer named (test: a run without `--guest-backup-storage` removes both backup lines); the **second-storage check** (D-405, M2 of #321): every other storage with a Plumb `AllocateSpace` line whose `content` has `backup` → the installer refuses with the fix, and the settings view shows it at run time; the installer (root) warns about backups of VMIDs in Plumb's range on the backup storage that are not Plumb's (L2 of #321); `PLUMB_PVE_EXTRA_NETS`, `PLUMB_PVE_GUEST_BACKUP_STORAGE`, `PLUMB_PVE_GUEST_BACKUP_CLEAR`; settings ACL check knows the lines | pl-backend | **pl-reviewer** | M | C1 | `install/install.sh`, `install/testdata/**`, `internal/driver/proxmox/acl*.go`, `internal/api/settings*.go`, `cmd/plumb/serve.go` (**first**) | `make test` (install fakes) + host C9.1 |
| C3 | Driver + plan: `netN` with Plumb's MAC, tag, hot-plug add/remove, update keeps MACs, server check `network-not-allowed`, risk of NIC changes, LXC `netN` `ip=`/`gw=`, firewall Block text (input on every interface, checked) | pl-backend | pl-architect | L | C1, C2 (env) | `internal/driver/{driver,guard*}.go` (**first**), `internal/driver/proxmox/{create,update,nics*}.go`, `internal/driver/fake/**`, `internal/plan/nics*.go`, `internal/plan/propose.go` (**first**), `blocks/firewall.yaml` (text only) | `make test`; host C9.2 |
| C4 | `network` resource for NIC N: match by MAC (D-403), `permaddr` rule (D-370), ip/dhcp/no-address per NIC; render from `guest.nics` | pl-reviewer (implementer) | pl-architect | M | C1, C3 (MAC function) | `internal/resource/network*.go`, `internal/state/render_nics*.go` | **VM guest job** (netplan; skips locally) |
| C5 | Backups: driver `vzdump` (notes-template, no protected/prune/bwlimit; for an LXC `exclude-path` for `/etc/plumb-agent` and `/var/lib/plumb-agent` if C9.11 shows it needs no other right, L1 of #321), list with marker read-back, delete one volume, storage status + encryption read (`GET /storage`), job runner + schedule (waits in restored mode, D-303), retention of marked lineage volumes only, free-space gate, migration **0025** (`guest_backup_lineage`, `guest_backup_run`), routes list/back-up-now/delete | pl-architect (implementer) | **pl-reviewer** | L | C1, C2, `driver.go` after C3 | `internal/driver/proxmox/backup*.go`, `internal/driver/fake/backup*.go`, `internal/backup/guest*.go` (new), `internal/store/migrations/0025_guest_backup.sql`, `internal/store/guestbackup*.go`, `internal/api/guestbackup*.go`, route lines in `api.go` (**first**), `cmd/plumb/serve.go` (after C2) | `make test`; race job; host C9.3–C9.5 |
| C6 | Restore over the same bound guest (D-410): Plan origin `restore`, reason `guest-restore`; the guest is stopped, marker + lineage + binding checked in the Run, revoke, then re-enroll Plan or install command. No restore after the guest is gone (D-411) | pl-backend | **pl-reviewer** | L | C5 | `internal/plan/restore_guest*.go`, `internal/plan/humanonly.go`, `internal/plan/propose.go` (after C3), route line in `api.go` (after C5), `internal/driver/proxmox/restore*.go` | `make test`; host C9.6 |
| C7 | Web: NIC list in the Target form, backups tab on the Target (state, encryption, items by kind, back up now, delete with confirm, restore wizard with the human-only text); the note for an object parameter whose nested defaults make it look fixed (#283 n1, ROADMAP row 32) | pl-frontend | pl-architect | M | C1 (mock), C5, C6 | `web/src/screens/{TargetForm,TargetBackups}.svelte` (new tab), `web/src/lib/{router,client,http}.ts`, `web/src/lib/mock/**`, e2e | `make test` (vitest, e2e) |
| C8 | `pve-dev`: make `vmbr9` (D-414): a card by **pl-lead**, exact stanza, undo | pl-backend (host) | **pl-reviewer** | S | – (**now**) | `docs/tasks/T-NNN.log.md` | host |
| C9 | Real-host acceptance (below) | pl-backend | **pl-reviewer** | M | C2–C7, C8 | `docs/tasks/T-NNN.log.md` | host |
| C10 | QA: risks §6 tests, VMID reuse, foreign volume, two agents, NIC rights, mutants per card | pl-codex-qa | – | M | per card | `*_qa_test.go` next to each card's files | `make test` |
| C11 | Slice 6 follow-ups (ROADMAP rows 28 … 32; row 33 is dropped). One PR per row, each ≤ ~150 lines | pl-codex-fix | pl-backend | S | – (**now**; row 32's field after C1) | row 28: the named test files and `.github/workflows/ci.yml` (one vet line); row 29: `internal/store/migrations/0026_unbind_pending_strict.sql`; row 30: `internal/{issue,store}/*_test.go` env helpers; row 31: `internal/store/issue.go`, `internal/issue/targetgone.go`; row 32: `web/src/screens/{General,TargetAgent}.svelte`, `internal/repair/ssh_key.go` (the #283 n1 note is in C7, which owns `TargetForm.svelte`) | `make test`; the race job for row 30 |
| C12 | README: more NICs, guest backups (encryption, what a backup holds, what stays after a Target delete), U7 note; **one plain sentence that the token itself can back up, restore over and delete pool guests' backups at the PVE API** (M3 of #321); a guest that is gone: restore it in Proxmox into pool `plumb`, then adopt it (D-411) | pl-frontend | pl-architect | S | C9 | `README.md` | – |

**Shared files and their order (D-371 (4)):**
- `api/openapi.yaml`, `api.gen.ts`, `target.json` → C1 only.
- `install/install.sh` → C2 only.
- `cmd/plumb/serve.go` → C2 → C5.
- `internal/driver/driver.go` → C3 → C5.
- `internal/plan/propose.go` → C3 → C6.
- `internal/plan/humanonly.go` → C6 only.
- `internal/api/api.go` → C5 → C6.
- `internal/resource/network*.go` → C4 only.
- `web/src/lib/{router,client}.ts` → C7 only.
- `internal/store/plan.go` (`DeleteBinding`) → C5 (drops its rows, rule 2).

**Parallel at once:** C1, C8, C11. After C1: C2 and C3 (C3 reads the env of C2 behind an interface; the files do not overlap except `serve.go`, which C3 does not touch). Then C4 after C3; C5 after C2; C6 after C5; C7 after C5 and C6. Codex cards (C10, C11) stay at ≤ ~150 lines each.

**C8 detail (D-414, pl-lead writes the card; the only network change on `pve-dev`).**
B2 of #321: never `ifreload -a` (it applies the whole file and could change `vmbr0`, the host's SSH path).
1. pl-lead makes sure a console path to `pve-dev` exists before the change.
2. Copy `/etc/network/interfaces` to a 0600 file outside the repo.
3. Run `ifquery -a -c` (running state against the file). **Stop** if anything differs.
4. Record `ip -br link`, `ip -br addr` and `ip route` (before).
5. Append the stanza:
  ```
  auto vmbr9
  iface vmbr9 inet manual
      bridge-ports none
      bridge-stp off
      bridge-fd 0
      bridge-vlan-aware yes
      bridge-vids 2-4094
  ```
6. Bring up **only** `vmbr9`: `ifup vmbr9`. Check `ip -d link show vmbr9` (`vlan_filtering 1`, no port).
7. Record link, addresses and routes again (after). Only `vmbr9` may differ. **Stop and undo** if anything of `vmbr0` (or any other line) differs.
8. **Undo:** `ifdown vmbr9`, remove the stanza (or put the copy back), check that `vmbr9` is gone, and compare before = after again.
- No address, no port, nothing else.

### Migrations
- **0025** `guest_backup` (C5): `guest_backup_lineage (target PK, lineage, created_at)` and `guest_backup_run (id, target, lineage, started_at, finished_at, result, volid, message)`.
- **0026** `unbind_pending` `STRICT` rebuild (C11, ROADMAP row 29); every row kept (test).
- No other migration: NICs are document fields; the allowed bridges are env (C2).
- pl-lead renumbers at the merge if needed (D-337). T-212 took none (start sweep).

### Rules (lessons of slice 6, D-413)
1. **Real link:** every new agent command type goes through the real link in a test (`TestEveryCommandTypeThroughRealLink` fails otherwise). Slice 7 plans none; a card that adds one adds it there.
2. **Per-Target state:** every new per-Target table says what happens at delete, at unbind and at VMID reuse, in its migration comment and in the PR:
   - `guest_backup_lineage`: dropped in the `DeleteBinding` transaction, so a new Target of the name starts a new lineage.
   - `guest_backup_run`: dropped with it.
   - The Proxmox volumes stay, D-408.
3. **Token host step:** every human-only rule that a token could pass has a real-host step with a token (`guest-restore` C9.6; the delete of a backup C9.5; `network-not-allowed` C9.2).
4. **Name the proving CI job:** local green is not proof where tests skip (netplan, root, race, no Docker). Each card names its job above; the PR links that job's run before review.
5. **Push rule** (memory "CI runner scarce"): push a PR branch only with complete, merge-needed changes.

### Risks
- **The token reaches another network:** one line per named bridge or bridge/tag; never propagate 1; the server refuses an unnamed one before PVE (C3 test with a fake PVE that would accept it).
- **A VM NIC hot-plug fails half way:** the driver reads the config back; the Run fails with the state shown; a second Plan converges.
- **NIC N MAC collision:** the MAC is derived from the install id, the Target and the NIC index, with the local bit (D-400). A collision needs the same three inputs.
- **The `network` resource matches the wrong NIC:** match by MAC only, `permaddr` rule; no match → refused, nothing changed (C4 test).
- **A backup is a copy of every secret in the guest:**
  - it holds the agent credential (revoked at restore), the desired list with secret values (0600), and the SSH host keys;
  - clear storage only by the install flag (D-406); the UI and README say what a backup holds;
  - C9.7 greps a real vzdump for the credential and a secret value, and records the result.
- **VMID reuse / foreign volumes:** only volumes with this install's marker and the Target's current lineage count. A new Target on an old VMID sees "older guest", never "plumb". Retention never touches another lineage or an unmarked volume. C10: a foreign volume with the same name pattern stays untouched.
- **Two live agents after a restore:** the credential is revoked in the restore Run before the guest starts; the old one gets 401 (C9.6).
- **The marker fails silently** (vzdump only warns): the volume is read back; no marker → `unmarked`, the job ends `warning` (D-396 style), and retention never deletes it.
- **The storage fills:** keep ≤ 30, the free-space gate (< 10 % free → the job is refused with the reason), one job per Target at a time.
- **The token is the second path (M3 of #321):** `guest-restore` is human-only inside Plumb, but the D-380 rights let a token holder back up, restore over and delete pool guests' backups straight at the PVE API (accepted by the user in U4). ARCH §20 (C1), the README (C12) and U8 say so in one sentence.
- **A second backup storage (M2 of #321):** `VM.Backup` lets the token write a vzdump to any storage with a Plumb `AllocateSpace` line and `backup` content. C2 refuses such a storage at install and the settings view shows it at run time.
- **Restore over a live guest is data loss:** human-only, `destructive`; the Plan names the volume and its time; the guest is stopped first (`allow_restart`).
- **Restored mode:** guest backup jobs wait (D-303); a restore Plan is refused while restored.

### Real-host acceptance (C9, `pve-dev`, controller on the latest main, after C8)
1. **Installer:** `--dry-run --extra-vlan vmbr9:20 --extra-bridge vmbr9 --guest-backup-storage plumb-backup --guest-backup-clear` prints exactly the new lines (D-401, D-405); the real run (D-180) writes them; the settings ACL view shows each as ok. A repair run without the flags removes them.
2. **NICs:** a Debian 13 VM and a Debian 13 LXC get a second NIC on `vmbr9` tag 20 with a static IP: both guests reach each other on that VLAN. Add and remove the NIC on the running VM (hot-plug). A `plan` token proposes a NIC on `vmbr9` tag 30 → 422 `network-not-allowed`. An adopted Target with `nics` → 422.
3. **Backup:** "Back up now" by an `apply` token for the VM and the LXC; the list shows `kind: plumb` with the marker; the log lines say "freeze skipped" (VM) and "snapshot" (LXC) (T-214 FACT 3).
4. **Schedule and retention:** schedule 2 minutes ahead with `keep: 2`; after three jobs only two marked volumes remain; a hand-made vzdump of the same VMID (no marker) stays.
5. **Delete one backup:** an `apply` token gets 403; an admin session deletes it.
6. **Restore over:** a `plan` token proposes, an `apply` token gets 403 (`guest-restore`), a person approves. The guest is restored and the old credential gets 401. The agent re-enrolls (VM by the re-enroll Plan; adopted LXC by the install command), and the next Run converges to the head.
7. **Leak hunt:** grep a real vzdump of the VM for the agent credential and a test secret value, and record the result (expected: present in clear on the clear storage, which is the D-406 warning). Grep the controller DB, events, logs and export for the volume notes: no secret.
8. **VMID reuse:** delete the VM Target (the guest goes, the backups stay). A new Target gets the same VMID: its list shows the old volumes as `older-guest`, and restore and retention refuse them.
9. **The rights limit (B1 of #321):** delete a pool guest in Proxmox by hand (outside Plumb): its backups are invisible to the token (`GET …/content` with the token does not list them), as T-214 FACT 1 says. Its Target shows `guest:missing` and offers no restore.
10. **Second storage (M2 of #321):** turn on `backup` content on the disk storage: the settings view shows the warning; a repair run of the installer refuses until it is off again (then turn it off).
11. **LXC `exclude-path` (L1 of #321):** an LXC backup with `exclude-path` for `/etc/plumb-agent` and `/var/lib/plumb-agent` works with the token (no extra right) and the archive does not hold them; record it. If it needs another right, C5 drops the option and says so.
12. **Host clean:** remove the test guests and backups; `vmbr9` stays until pl-lead's undo card.

## Result (owner, max 8 lines)
branch · commit · tests · notes

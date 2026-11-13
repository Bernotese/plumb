# T-107 – Plan for slice 4

- **State:** plan updated after review round 1 (PR #206); waits for pl-reviewer, then the user.
- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** –

## Goal
Write the plan for slice 4. The user approves it before any code exists ("Ohne Freigabe entsteht kein Code").

## Context (read only these)
- `docs/BRIEF.md` §5 and the open points in §6
- `docs/STATUS.md`
- `docs/tasks/T-089-slice-3-plan.md` – format to copy, the leftovers and the slice 4 proposal at its end (Proxmox day-2, scheduled backups + restore proof, `network`/`firewall` resources, raw-mode offer on Issues)
- `docs/DECISIONS.md` – only what you need (token rights: D-081, D-239, D-240; D-227)
- Leftovers: 2 low `npm audit` findings (`deepmerge`, dev-only), GO-2026-5932, the Ubuntu 26.04 limit, flaky `cmd/plumb TestServeNoAnswer`

## Acceptance criteria
- [ ] This card holds the plan in the T-089 format: scope, gap list against the BRIEF, what is out, child cards (owner, size, dependencies, what runs in parallel), risks, real-host acceptance
- [ ] Security first: say for each part which token rights it needs. A new right is a user question (D-081), never a default
- [ ] At most 6 questions for the user, each with one recommendation and one sentence of reason. Plain words; the user reads a German summary from pl-lead
- [ ] Proposed decisions in `docs/decisions/T-107.md` as `proposed`; ask pl-lead for the numbers (next free: D-287)
- [ ] Budget rule kept: Codex cards are narrow (≤ ~150 lines)
- [ ] PR with the card only; pl-reviewer checks it; no code

## Out of scope
- Any code, schema or contract change

## Plan (architect)

Review round 1 (pl-reviewer, PR #206): H1, H2, M1 … M6, L1 … L6 taken in; see "Changes after review" at the end.

### For the user (one screen)
**Proposal: slice 4 = "Safe day 2".** Plumb builds and configures guests, but three things of the brief are still missing: a backup of Plumb itself, changes to a running guest at the Proxmox level, and the `firewall` and `network` resources. A lost controller today means lost history, lost secrets and guests that Plumb no longer knows. So backups come first.

Slice 4 holds, in this order:
1. **Backup and restore of Plumb.** A scheduled, encrypted backup of the whole controller state (daily, keep 7). A host timer copies only this encrypted file off the Plumb container. A new "replace the controller" path keeps your guests. A restored Plumb knows all guests again, but changes nothing until you confirm that the old controller is off. The proof is a real restore on the test host, with guests.
2. **Proxmox day 2:** start/stop as desired state (`power`), Proxmox tags, disk grow (VM and LXC; the agent grows the file system in a VM).
3. **`firewall` resource with a safe apply:** when the agent cannot reach the controller after the change, it puts the old rules back by itself. A rule set that closes SSH is refused unless you say so explicitly.
4. **Raw mode on Issues and Targets:** an Issue that Block parameters cannot express says "Accept as raw for this Target"; the Target shows its raw resources, lets you edit them and offers "back to the Block".
5. QA, one flaky test, one real-host acceptance run.

**No new token right** in slice 4 if you take the recommendations (table "Token rights" below).
**Later:** more NICs and VLAN tags, guest adoption, guest backups by Plumb, SSH repair fallback, signed image checksums, MCP, AI help (and `network`, if you take Q5 (b)).

**Questions for the user**
1. How does the backup leave the host? (a) a timer on the host copies only the encrypted Plumb backup file to a storage you name **(recommended)** · (b) a Proxmox backup (vzdump) of the whole Plumb container · (c) the Plumb token gets `VM.Backup` and runs the backup itself. *(b) and (c) put the Proxmox token secret, the CA key and the database in clear on the backup storage (only an encrypted Proxmox Backup Server protects them); (a) needs no new right and copies only encrypted data.*
2. What is in a backup? (a) the whole database (history, guest links, agent identities, CA), encrypted with the master key **(recommended)** · (b) only the export. *With only the export, a restored Plumb does not know its guests and would make new ones.*
3. Guests that the restored Plumb does not know (made after the backup)? (a) Plumb lists them and touches nothing **(recommended)** · (b) Plumb adopts them. *Adoption is a big feature of its own; a list is safe and enough for a homelab.*
4. Firewall: (a) nftables inside the guest, managed by the agent **(recommended)** · (b) the Proxmox firewall per guest. *(b) works only when the datacenter firewall is on, and the token cannot turn it on; (a) is the same in VM and LXC.*
5. The `network` resource: (a) in slice 4, only routes, DNS servers and extra addresses on the one NIC (the main address stays as made) · (b) later, together with more NICs **(recommended)**. *With one NIC and a fixed main address there is little to manage, and each network change can cut the guest off.*
6. More NICs and VLAN tags: (a) later **(recommended)** · (b) in slice 4. *Each bridge needs its own `SDN.Use` grant (a new right by D-081), and the test host has one bridge.*

### Gap list: BRIEF §5/§6 vs. `main`
| Item | State | Proof |
|---|---|---|
| 5.1 installer | done | T-013, T-052, T-053, PR #98 |
| 5.2 all screens incl. Blocks, plan preview, live progress, history, Issues | done | T-006, T-014, T-096 |
| 5.2 raw editing (e.g. a config file) where it makes sense | **partly**: a Block is edited as YAML text (T-096); an `overrides` value of a Target has no editor | D-261, ARCHITECTURE §3 |
| 5.3 revisions, rollback, export/import, secret store | done | slice 2 part A, T-037 |
| 5.3 scheduled backups; a restored Plumb reconciles | **missing** | no backup code; `plumb key restore` exists (`cmd/plumb/key.go`) |
| 5.4 images: download, build, seal, retention, schedule | done | T-039, T-082 |
| 5.5 create, cores/memory, delete (VM, LXC) | done | T-053, T-056, T-079 |
| 5.5 start/stop, tags, disk change | **missing** (`disk` create-only) | `internal/state/schema/target.json` |
| 5.5 more NICs | **missing** | one `bridge`, `PlumbBridge` on one path (D-081) |
| 5.6 resources | 13 of the 15 brief items (template = `file`, group and SSH key in `user`, timer = `unit`); **missing** `network`, `firewall` | `internal/resource/schema`, D-260 |
| 5.6 UI hides what does not apply | **partly**: save refuses a resource on a wrong target kind (e.g. `sysctl` on lxc); the Target form does not filter Blocks by kind (no such code found in `web/src/screens/TargetForm.svelte`) | D-260; C8 checks and adds the filter |
| 5.6 Docker Compose Block, agent updates by Plumb | done | T-095, T-104, T-091, T-092 |
| 5.7 starter catalogue | done except a firewall Block | `blocks/` |
| 5.8 drift, Accept/Revert/Ignore | done | T-018, T-032, T-079 |
| 5.8 raw-mode offer | **partly**: Accept writes `overrides`, but nothing says "raw" and nothing leads back | ARCHITECTURE §3, §6 |
| 5.9 API, risk classes, token tiers, events, provenance | done; MCP/AI later | T-011, T-030 |
| §6 open questions | all answered | ARCHITECTURE §1–9 |

### Token rights per part (D-081: a new right is a user question)
| Part | Proxmox right | Has it today? |
|---|---|---|
| Backup in the controller (file in `/var/lib/plumb/backups`) | none (inside the LXC) | – |
| Off-host copy, Q1 (a): host timer copies the encrypted file | none for the token: the installer (root) makes the timer; the timer runs as root on the host | – |
| Off-host copy, Q1 (b): vzdump job of the LXC | none for the token (installer makes the job) | – but secrets in clear (H1) |
| Off-host copy, Q1 (c) | `VM.Backup` on the LXC + `Datastore.AllocateSpace` on a backup storage | **no: new right** |
| Show the state of the off-host copy | none: the timer writes a status file into the LXC (`pct push`); `Sys.Audit` on `/` (D-240) would also read vzdump jobs | – |
| Replace the controller, restore | none for the token: a person runs the installer as root; the restore makes a new token (the old secret is not in the backup) | – |
| Start/stop | `VM.PowerMgmt` on the pool | yes (D-081) |
| Tags | `VM.Config.Options` on the pool | yes; a host check: a datacenter `user-tag-access` other than `free` can refuse it |
| Disk grow | `VM.Config.Disk` on the pool, `Datastore.AllocateSpace` on the disk storage | yes |
| More NICs / VLAN tag | `SDN.Use` on each more bridge path, or on the zone with VLANs | **no: new ACL** → later (Q6) |
| `firewall`, `network` (agent, in the guest) | none (the agent runs as root in the guest) | – |
| Firewall Q4 (b) | `VM.Config.Network` (has it) + the datacenter firewall on (`Sys.Modify` on `/`) | **no** → not recommended |
| Raw mode | none | – |

### What is out of slice 4
- Backups of the guests: Proxmox backup jobs do this well; Plumb shows nothing about them.
- Adoption of unknown guests (Q3), change of the main `ip` of a Target (stays create-only), disk shrink (Proxmox cannot), more NICs and VLAN tags (Q6), `network` with the recommendation of Q5, backup download through the API (a backup holds the CA key and the agent identities, even if encrypted), a backup copy to a Proxmox Backup Server (Q1 (a) needs a storage with a path: `dir`, `nfs`, `cifs`, `cephfs`).

### Known leftovers
| Item | Verdict | Reason |
|---|---|---|
| 2 low `npm audit` (`deepmerge`, dev-only) | later | not in `web/dist`; next dependency refresh |
| GO-2026-5932 (`x/crypto/openpgp`, module-only) | later | not imported, not called; the fix exists only as a pseudo-version |
| Ubuntu 26.04 limit (D-192, D-220, D-223) | later, stays documented | workaround: a Plumb-built Ubuntu Image or DHCP |
| flaky `cmd/plumb TestServeNoAnswer` | slice 4, C11 | a flaky test hides real failures in the CI that slice 4 changes most (`serve`) |

### Child cards (numbers from pl-lead)
| # | Title | Owner | Reviewer | Size | Depends on | Parallel |
|---|---|---|---|---|---|---|
| C1a | Contract backup/restore: backup file format (L5), API (`GET /backups`, `POST /backups`, state of the off-host copy, no download), `plumb restore`, installer flags (`--backup-storage`, `--remove --keep-guests`, `--restore`), restored mode (API 409 `restored-mode`, what is stopped, D-290), Issues `guest:missing` and `agent:silent` | pl-architect | pl-reviewer | M | – | first |
| C1b | Contract day 2 + firewall: `guest.power`, `guest.tags`, grow-only `guest.disk`, agent command `grow`, safe-apply wire (`confirm` window, result `rolled-back`), schema of `firewall` (SSH rule, D-295), raw-mode marks on Issues and the override editor | pl-architect | pl-reviewer | M | – | with C1a |
| C2 | Backup in the controller: online SQLite snapshot + link dir, chunked AEAD under a key derived from the master key, schedule + retention, API, events | pl-backend | pl-reviewer | M | C1a | with C5, C6 |
| C3 | Restore: `plumb restore`; restored mode in API, engine, schedules and agent link; on restore drop sessions, disable API tokens, force a new admin password (D-290); `guest:missing`, `agent:silent`, unknown guests | pl-backend | pl-reviewer | L | C2 | after C2 |
| C4 | Installer: backup copy timer (Q1 (a)), `--remove --keep-guests`, `--restore <file>` (recovery key on a TTY, same address, no DHCP, new token), dry run and repair run of each | pl-reviewer as implementer | **pl-architect** | M | C1a, Q1 | with C2 |
| C5 | Proxmox day 2 in driver and engine: `power`, `tags`, disk grow (VM resize + agent `grow`; LXC resize; disk gate D-219), risk classes, drift, steps on a stopped Target | pl-reviewer as implementer | **pl-architect** | L | C1b, C7 for the VM grow end to end | with C2 |
| C6 | Agent: safe apply (round trip after the change, else restore; the guard of D-271 covers an agent crash) + resource `firewall` + CI guest tests incl. a forced rollback and nftables in an LXC | pl-backend | pl-reviewer | L | C1b | with C5 |
| C7 | Agent command `grow` (growpart + `resize2fs`/`xfs_growfs` on the root file system only) + guest test | pl-codex-fix | pl-reviewer | S | C1b | with C6 |
| C8 | Web: Settings → Backups (list, state, off-host copy state, "back up now"), restored-mode banner + confirm + token re-enable, Target fields `power`/`tags`/disk grow, raw mode on Issue and Target (list, edit, back to the Block), Block filter by target kind | pl-frontend | pl-reviewer | L | C1a, C1b (mock), C2, C3, C5 | from C1 on |
| C9 | Starter Block `firewall` (package `nftables`, SSH + listed ports open, rest of input closed) | pl-frontend | pl-reviewer | S | C6 | after C6 |
| C10 | QA per card: tests against C2–C9; mutation audit of the backup crypto, the restored mode and the safe-apply rollback | pl-codex-qa | – | M | each card | per card |
| C11 | Fix flaky `TestServeNoAnswer` (find the race, fix the test or the code; 200 runs green) | pl-codex-fix | pl-reviewer | S | – | now |
| C12 | Real-host acceptance: backup → off-host copy → `--remove --keep-guests` with a VM and an LXC in the pool → `--restore` → restored mode, confirm, agents come back, Plan clean, revoked token stays off, `guest:missing` and unknown guests shown; power/tags/grow on VM and LXC; a `firewall` change that cuts the link rolls back by itself; a `drop` without SSH is refused | pl-backend | pl-reviewer | M | C2–C9 | last |

Codex cards (C7, C11, C10 parts) are narrow: one command, one test, one audit per card. C6 is too big for Codex (it touches the agent loop). pl-reviewer implements C4 and C5, so pl-architect reviews those two (M6). If the user takes Q5 (a), a card C6b `network` (pl-backend, M, after C6) is added, with a CI probe of netplan in both cloud images first.

### Risks and the real test host
- **Two controllers at once.** A restore while the old controller still runs gives two controllers with the same CA and agent identities. Mitigation: restored mode (D-290). Until a person confirms that the old controller is off, the restored Plumb sends no Proxmox write and no agent command except observe; no Plan is approved; image schedules, retention, agent updates, Revert and the backup schedule wait.
- **Revoked access comes back.** A token revoked after the backup, an old admin password and old sessions are in the restored database. Mitigation (D-290): the restore drops all sessions and disables every API token; a person re-enables tokens one by one in restored mode; the admin must set a new password at the first login.
- **Replace the controller with guests in the pool.** Today `--remove` refuses while the pool has guests (`install/install.sh:1412`). New: `--remove --keep-guests` removes only the Plumb LXC, its key volume, the copy timer and the Plumb token; it keeps pool, guests, templates, user, roles, ACLs and storages. `--restore` then installs into this pool and accepts its guests. C12 tests it with a VM and an LXC in the pool.
- **Same address needed.** Agents pin the controller URL (D-082). `--restore` refuses another LXC address than the one in the backup and refuses `--lxc-ip dhcp` (D-179).
- **The backup holds the CA key and agent identities.** It is encrypted with a key derived from the master key, which is never in it (D-177). The Proxmox token secret is not in it; the restore makes a new token. Without the recovery key a backup is useless, also for an attacker. The copy timer copies only the encrypted file (H1); a vzdump of the container (Q1 (b)) would put `/etc/plumb/plumb.env`, the link dir and the database in clear on the storage.
- **State after the backup.** A Target deleted after the backup: its guest is gone, the restored Plumb opens `guest:missing` and never re-creates it by itself. A guest made after the backup: unknown, listed, not touched. A bound guest whose agent re-enrolled after the backup: its credential is unknown; no hello within 10 minutes after the confirm opens `agent:silent` with the re-enroll action. Secrets changed after the backup are old again: the restore page says so.
- **`firewall` can lock out SSH and the agent.** Plumb owns one nftables table (`inet plumb`); input filter only; it always keeps loopback and `established,related` (the agent link is outbound, so the safe apply does not see an SSH lockout). So the schema refuses policy `drop` without an accept rule for the SSH port, unless `allow_no_ssh: true`. The package `nftables` must be installed (the starter Block does it). nftables in an unprivileged LXC must be proven in CI and on the host.
- **`power: stopped`**: a stop is `disruptive` and needs `allow_restart` on VM and LXC Targets. On a stopped Target, agent resource steps are `skipped` with "Target stopped" and an offline agent is no Issue; a Plan that sets `power: running` starts the guest before its agent steps.
- **Disk grow cannot be undone**: grow only, a smaller value is refused at save; it passes the disk gate (D-219, thin-pool overcommit); risk `disruptive` (D-291 says why). The agent grows only the root file system when it is the last partition; else the step fails with a clear text.
- Host needed for C4 and C12 (one host agent at a time, named by pl-lead). C2, C3, C5–C7 run in CI first (fake PVE, CI guest tests).

### New decisions (proposed; full text in `docs/decisions/T-107.md`)
D-287 scope and order · D-288 backup content, format and encryption · D-289 off-host copy by a host timer · D-290 replace the controller, restore and restored mode · D-291 Proxmox day 2 (`power`, `tags`, disk grow) · D-292 more NICs and VLAN tags later · D-293 safe apply · D-294 `network` later (Q5) · D-295 `firewall` resource · D-296 raw mode on Issues and Targets.

### Changes after review (round 1)
- H1: Q1 re-worded; D-289 is now a host timer that copies only the encrypted file; the vzdump option says what it holds in clear.
- H2: `--remove --keep-guests` and `--restore` into the kept pool (D-290, C1a, C4, C12).
- M1, M2, M3: in D-290 (sessions, tokens, admin password; `agent:silent`; restored mode stops every writer).
- M4: Q5 is now `network` now or later (recommended later, D-294); the old Q5 (fs grow) is the default of D-291.
- M5: SSH rule and `nftables` package in D-295, C9. M6: C6 → pl-backend, C5 → pl-reviewer, pl-architect reviews C4 and C5.
- L1: C1 split into C1a, C1b; the netplan probe moves to C6b (only with Q5 (a)). L2: C5 depends on C7, C4 on Q1. L3, L4: in D-291. L5: in D-288, D-290. L6: two gap rows added.

## Result (owner, max 8 lines)
branch · commit · tests · notes

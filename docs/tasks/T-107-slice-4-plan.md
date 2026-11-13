# T-107 – Plan for slice 4

- **State:** plan written, waits for pl-reviewer, then the user.
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

### For the user (one screen)
**Proposal: slice 4 = "Safe day 2".** Plumb builds and configures guests, but three things of the brief are still missing: a backup of Plumb itself, changes to a running guest at the Proxmox level, and the `network` and `firewall` resources. A lost controller today means lost history, lost secrets and guests that Plumb no longer knows. So backups come first.

Slice 4 holds, in this order:
1. **Backup and restore of Plumb.** A scheduled, encrypted backup of the whole controller state (daily, keep 7). A fresh Plumb restored from it knows all guests again and reconciles them. The proof is a real restore on the test host.
2. **Proxmox day 2:** start/stop as desired state (`power`), Proxmox tags, disk grow (VM and LXC; the agent grows the file system in a VM).
3. **`network` and `firewall` resources with a safe apply:** when the agent cannot reach the controller after the change, it puts the old state back by itself.
4. **Raw mode on Issues:** an Issue that Block parameters cannot express says "Accept as raw for this Target"; the Target shows its raw resources with "back to the Block".
5. QA, one flaky test, one real-host acceptance run.

**No new token right** in slice 4 if you take the recommendations (table "Token rights" below).
**Later:** more NICs and VLAN tags, guest adoption, guest backups by Plumb, SSH repair fallback, signed image checksums, MCP, AI help.

**Questions for the user**
1. Where does the backup leave the host? (a) the installer makes a Proxmox backup job of the Plumb container on a storage you name (also a Proxmox Backup Server) **(recommended)** · (b) the Plumb token gets `VM.Backup` and runs the backup itself · (c) Plumb uploads to S3/SFTP. *(a) needs no new token right and no new stored password; the key volume stays out of it (D-177).*
2. What is in a backup? (a) the whole database (history, guest links, agent identities, CA), encrypted with the master key **(recommended)** · (b) only the export. *With only the export, a restored Plumb does not know its guests and would make new ones.*
3. Guests that the restored Plumb does not know (made after the backup)? (a) Plumb lists them and touches nothing **(recommended)** · (b) Plumb adopts them. *Adoption is a big feature of its own; a list is safe and enough for a homelab.*
4. Firewall: (a) nftables inside the guest, managed by the agent **(recommended)** · (b) the Proxmox firewall per guest. *(b) works only when the datacenter firewall is on, and the token cannot turn it on; (a) is the same in VM and LXC.*
5. Disk grow in a VM: (a) Plumb grows the Proxmox disk and the agent grows the root partition and file system **(recommended)** · (b) Plumb grows only the Proxmox disk. *With (b) the guest does not see the space until a person acts.*
6. More NICs and VLAN tags: (a) later **(recommended)** · (b) in slice 4. *Each bridge needs its own `SDN.Use` grant (a new right by D-081), and the test host has one bridge.*

### Gap list: BRIEF §5/§6 vs. `main`
| Item | State | Proof |
|---|---|---|
| 5.1 installer | done | T-013, T-052, T-053, PR #98 |
| 5.2 all screens incl. Blocks, plan preview, live progress, history, Issues | done | T-006, T-014, T-096 |
| 5.3 revisions, rollback, export/import, secret store | done | slice 2 part A, T-037 |
| 5.3 scheduled backups; a restored Plumb reconciles | **missing** | no backup code; `plumb key restore` exists (`cmd/plumb/key.go`) |
| 5.4 images: download, build, seal, retention, schedule | done | T-039, T-082 |
| 5.5 create, cores/memory, delete (VM, LXC) | done | T-053, T-056, T-079 |
| 5.5 start/stop, tags, disk change | **missing** (`disk` create-only) | `internal/state/schema/target.json` |
| 5.5 more NICs | **missing** | one `bridge`, `PlumbBridge` on one path (D-081) |
| 5.6 resources | 13 of the 15 brief items (template = `file`, group and SSH key in `user`, timer = `unit`); **missing** `network`, `firewall` | `internal/resource/schema`, D-260 |
| 5.6 Docker Compose Block, agent updates by Plumb | done | T-095, T-104, T-091, T-092 |
| 5.7 starter catalogue | done except a network/firewall Block | `blocks/` |
| 5.8 drift, Accept/Revert/Ignore | done | T-018, T-032, T-079 |
| 5.8 raw-mode offer | **partly**: Accept writes `overrides`, but nothing says "raw" and nothing leads back | ARCHITECTURE §3, §6 |
| 5.9 API, risk classes, token tiers, events, provenance | done; MCP/AI later | T-011, T-030 |
| §6 open questions | all answered | ARCHITECTURE §1–9 |

### Token rights per part (D-081: a new right is a user question)
| Part | Proxmox right | Has it today? |
|---|---|---|
| Backup in the controller (file in `/var/lib/plumb/backups`) | none (inside the LXC) | – |
| Off-host copy, Q1 (a) | none for the token: the installer runs as root on the host and makes the backup job | – |
| Off-host copy, Q1 (b) | `VM.Backup` on the LXC + `Datastore.AllocateSpace` on a backup storage | **no: new right** |
| Restore | none for the token (a person runs the installer with `--restore` and the recovery key) | – |
| Start/stop | `VM.PowerMgmt` on the pool | yes (D-081) |
| Tags | `VM.Config.Options` on the pool | yes; a host check: a datacenter `user-tag-access` other than `free` can refuse it |
| Disk grow | `VM.Config.Disk` on the pool, `Datastore.AllocateSpace` on the disk storage | yes |
| More NICs / VLAN tag | `SDN.Use` on each more bridge path, or on the zone with VLANs | **no: new ACL** → later (Q6) |
| `network`, `firewall` (agent, in the guest) | none (the agent runs as root in the guest) | – |
| Firewall Q4 (b) | `VM.Config.Network` (has it) + the datacenter firewall on (`Sys.Modify` on `/`) | **no** → not recommended |
| Raw mode | none | – |

### What is out of slice 4
- Backups of the guests: Proxmox backup jobs do this well; Plumb shows nothing about them.
- Adoption of unknown guests (Q3), change of the main `ip` of a Target (stays create-only), disk shrink (Proxmox cannot), more NICs and VLAN tags (Q6), backup download through the API (a backup holds the CA key and the agent identities, even if encrypted).

### Known leftovers
| Item | Verdict | Reason |
|---|---|---|
| 2 low `npm audit` (`deepmerge`, dev-only) | later | not in `web/dist`; next dependency refresh |
| GO-2026-5932 (`x/crypto/openpgp`, module-only) | later | not imported, not called; the fix exists only as a pseudo-version |
| Ubuntu 26.04 limit (D-192, D-220, D-223) | later, stays documented | workaround: a Plumb-built Ubuntu Image or DHCP |
| flaky `cmd/plumb TestServeNoAnswer` | slice 4, C11 | a flaky test hides real failures in the CI that slice 4 changes most (`serve`) |

### Child cards (numbers from pl-lead)
| # | Title | Owner | Size | Depends on | Parallel |
|---|---|---|---|---|---|
| C1 | Contract: backup file format and API (`GET /backups`, `POST /backups`, status, no download), `restore` CLI, restored mode; `guest.power`, `guest.tags`, grow-only `guest.disk`; agent command `grow`; safe-apply wire (`confirm` window, result `rolled-back`); schemas of `network` and `firewall`; raw-mode marks on Issues. Check that the Debian 13 and Ubuntu 26.04 cloud images use netplan (one CI guest probe) | pl-architect | M | – | first |
| C2 | Backup in the controller: online SQLite snapshot + link dir, encrypted with a key derived from the master key, schedule + retention, API, events | pl-backend | M | C1 | with C5, C6 |
| C3 | Restore: `plumb restore` + installer `--restore <file>` (recovery key on a TTY, D-178 rules); restored mode (observe only until a person confirms "the old controller is off"); missing guest → Issue, unknown pool guest → list | pl-backend | L | C2 | after C2 |
| C4 | Installer: optional Proxmox backup job of the Plumb LXC (`--backup-storage`, `--backup-schedule`), dry run, repair run, `--remove` removes the job | pl-reviewer as implementer | S | C1 | with C2 |
| C5 | Proxmox day 2 in driver and engine: `power`, `tags`, disk grow (VM resize + agent `grow`; LXC resize), risk classes, drift | pl-backend | L | C1 | with C2 |
| C6 | Agent: safe apply (round trip after the change, else restore; the guard of D-271 covers an agent crash) + resources `network` (VM only) and `firewall` + CI guest tests incl. a forced rollback | pl-reviewer as implementer | L | C1 | with C2, C5 |
| C7 | Agent command `grow` (growpart + `resize2fs`/`xfs_growfs` on the root file system only) + guest test | pl-codex-fix | S | C1 | with C6 |
| C8 | Web: Settings → Backups (list, state, "back up now", restored-mode banner and confirm), Target fields `power`/`tags`/disk grow, raw mode on Issue and Target | pl-frontend | L | C1 (mock), C2, C3, C5 | from C1 on |
| C9 | Starter Block `firewall` (SSH + listed ports open, rest of input closed) | pl-frontend | S | C6 | after C6 |
| C10 | QA per card: tests against C2–C9; mutation audit of the backup crypto, the restored mode and the safe-apply rollback | pl-codex-qa | M | each card | per card |
| C11 | Fix flaky `TestServeNoAnswer` (find the race, fix the test or the code; 200 runs green) | pl-codex-fix | S | – | now |
| C12 | Real-host acceptance: backup → `--remove` → fresh install with `--restore` → agents come back, Plan clean, missing/unknown guests shown; Proxmox backup job runs; power/tags/grow on VM and LXC; a `network` and a `firewall` change that cut the link roll back by themselves | pl-backend | M | C2–C9 | last |

Codex cards (C7, C11, C10 parts) are narrow: one command, one test, one audit per card. C6 is too big for Codex (it touches the agent loop).

### Risks and the real test host
- **Two controllers at once.** A restore while the old controller still runs gives two controllers with the same CA and agent identities. Mitigation: a restored Plumb starts in restored mode: it observes and opens Issues, but no Plan is approved until a person confirms that the old controller is off. Only the host shows real agents reconnecting (C12).
- **Same address needed.** Agents pin the controller URL (D-082). A restore must use the same LXC address; the installer refuses `--restore` with another address and says why. DHCP installs (D-179) can lose this.
- **The backup holds the CA key and agent identities.** It is encrypted with a key derived from the master key, which is never in the backup (D-177). Without the recovery key a backup is useless, also for an attacker.
- **State after the backup.** A Target deleted after the backup: its guest is gone, the restored Plan shows "guest missing" as an Issue and never re-creates it by itself. A guest made after the backup: its agent gets 401 (unknown credential) and the guest shows in the "unknown" list. Secrets changed after the backup are old again: the restore page says so.
- **`network` can cut the agent link.** Safe apply: the agent keeps the old file, applies, and needs a fresh round trip to the controller within 90 s; else it restores and reports `rolled-back`. If the agent crashes in the window, the guard timer of D-271 restores. A forced rollback is a CI guest test and a host test. LXC: Proxmox owns the network (§6), so `network` is VM only.
- **`firewall` can lock out SSH and the agent.** Plumb owns one nftables table (`inet plumb`) and nothing else; input filter only, it always keeps loopback and `established,related` (the agent link is outbound). Same safe apply. nftables in an unprivileged LXC must be proven in CI and on the host.
- **Netplan** must be in both cloud images (C1 checks it); if Debian 13 has no netplan, `network` writes systemd-networkd files instead, decided in C1.
- **`power: stopped`**: an offline agent of a stopped Target is expected, no Issue; a stop needs `allow_restart` semantics (D-194) for VMs: risk `disruptive`, shown in the Plan.
- **Disk grow cannot be undone**: grow only, a smaller value is refused at save; risk `disruptive`. The agent grows only the root file system when it is the last partition; else the step fails with a clear text.
- Host needed for C4 and C12 (one host agent at a time, named by pl-lead). C2, C3, C5–C7 run in CI first (fake PVE, CI guest tests).

### New decisions (proposed; full text in `docs/decisions/T-107.md`)
D-287 scope and order · D-288 backup content and encryption · D-289 off-host copy by an installer-made Proxmox backup job · D-290 restore and restored mode · D-291 Proxmox day 2 (`power`, `tags`, disk grow) · D-292 more NICs and VLAN tags later · D-293 safe apply · D-294 `network` resource · D-295 `firewall` resource · D-296 raw mode on Issues.

## Result (owner, max 8 lines)
branch · commit · tests · notes

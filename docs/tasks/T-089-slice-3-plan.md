# T-089 – Plan for slice 3

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M (plan only, no code)   · **Depends on:** –

## Goal
The user wants to go on. Propose what slice 3 holds, as a plan the user can approve. No code is written before the user approves.

## Context (read only these)
- `docs/BRIEF.md` §5 (what Plumb must do) and §6 (open questions) – find what is still missing after slices 1 and 2
- `docs/STATUS.md` – what is done, known limits
- `docs/ARCHITECTURE.md`, `docs/DECISIONS.md` – only the parts you need (use sub-agents for wide searches)
- Known leftovers that must each get a clear "in slice 3 / later / drop" with a reason:
  - two Images with the same base upload the repacked file twice
  - T-087 leftovers: 2 low `npm audit` findings (`deepmerge`, dev-only), GO-2026-5932 (module-only)
  - T-084: no proof with a real Image build Plan that the thin-pool metadata note shows
  - web mutation audit not done; boundary test for Content-Length == cap
  - the known Ubuntu 26.04 limit ("Open for the user" in STATUS)
  - old open Issues 14/15 on the test controller; stale worktrees (housekeeping, lead)

## Acceptance criteria
- [ ] Section "Plan" in this card: a gap list BRIEF §5/§6 vs. `main` (short table: item, state, proof)
- [ ] A proposed scope for slice 3: 1 theme or a few, with the reason for the order, and what stays for slice 4
- [ ] Child cards as a list (title, owner, size, depends on, parallel or not); no card files yet
- [ ] Risks and what needs the real test host
- [ ] "Questions for the user": at most 6, each with options and ONE recommendation and a one-line reason
- [ ] New decisions as proposals only (numbers from D-256; status "proposed")
- [ ] The whole plan is short: the user part fits one screen

## Out of scope
- Any code, migration, API or installer change
- New token rights (D-081): only as a question for the user

## Plan (architect, optional)

### For the user (one screen)
**Proposal: slice 3 = "Blocks for every day".** Today Plumb has 6 resource types, 5 starter Blocks and no Blocks screen. You cannot build a typical homelab guest (hardened SSH, tools, Docker Compose app) in the UI. That is the core promise of the brief (replace Ansible), so it goes first.

Slice 3 holds, in this order:
1. **Agent update by Plumb + version gate.** New resource types need a new agent on existing guests. Without this, old agents get resources they do not know (D-169 gap).
2. **Four new resources:** `apt_repository`, `unit` (systemd unit, also timers), `sysctl` (VM only), `command` (escape hatch, no drift).
3. **Four new starter Blocks:** `ssh-hardening`, `common-tools`, `shell`, `docker-compose`.
4. **Blocks screen:** list, view, new, edit (YAML with a schema check); save goes through a normal Plan.
5. QA debt (web mutation audit, Content-Length boundary test) and one real-host acceptance run.

**Slice 4 (proposed):** Proxmox day-2 (start/stop, tags, disk grow, more NICs), scheduled backups + a restore-and-reconcile proof, `network` and `firewall` resources (they can cut the agent link: need a safe apply), raw-mode offer on Issues. Later: SSH repair fallback, signed image checksums, MCP, AI help.

**Questions for the user**
1. Theme of slice 3? (a) Blocks for every day **(recommended)** · (b) Proxmox day-2 · (c) backups. *The Proxmox UI covers start/stop meanwhile; Blocks are the reason for Plumb.*
2. Agent updates? (a) `system` proposes a Plan, a person or token approves **(recommended)** · (b) automatic after a controller upgrade · (c) by hand per Target only. *Same plan → approve path as everything else, nothing silent.*
3. Docker source for `docker-compose`? (a) Docker's official apt repo, key pinned in the Block **(recommended)** · (b) distro packages. *Same package names on Debian 13 and Ubuntu 26.04 and a current Compose; (b) has different names per OS and needs a `when` feature.*
4. If Docker needs `keyctl` in an unprivileged LXC: (a) `docker-compose` is VM-only in slice 3 **(recommended)** · (b) allow `keyctl` (changes D-227 and the token rights, D-081). *No new token rights without a separate decision.*
5. `command` resource? (a) in slice 3, a Plan with it is always human-only **(recommended)** · (b) leave it out. *The brief asks for it; human-only keeps AI tokens away from arbitrary commands.*

### Gap list: BRIEF §5/§6 vs. `main`
| Item | State | Proof |
|---|---|---|
| 5.1 installer, restricted token, LXC, prints address | done | T-013/T-052, T-053, PR #98 |
| 5.2 screens for images, targets, issues, history, settings, tokens; plan preview, live progress | done | `web/src/screens`, T-006, T-014 |
| 5.2 Blocks screen, own Blocks in the UI | **missing** (API only: a proposal can hold a Block) | no Blocks route in `web/src/lib/router.ts` |
| 5.3 revisions, rollback, export/import, secret store | done | slice 2 part A, T-037 |
| 5.3 scheduled backups; restored Plumb reconciles | **missing** / not proven | no backup code |
| 5.4 download, build, seal, retention, schedule, version per Target | done | T-039, T-082, T-066, T-083 |
| 5.5 create, change cores/memory, delete | done | T-053, T-056, T-079 |
| 5.5 start/stop, tags, disk change, more NICs | **missing** (disk, ip create-only) | `schema/target.json` |
| 5.6 observe/diff/apply, parallel, strictness | done | resources, T-018, package `strictness` |
| 5.6 resources | 6 of ~15: file, directory, symlink, package, service, user (groups, ssh keys) | `internal/resource/schema` |
| 5.6 Docker Compose Block; agent updates by Plumb | **missing** | D-169 "no version gate yet" |
| 5.7 starter catalogue | 5: deploy-user, motd, nginx, nginx-site, app-env; no ssh, shell, tools, compose | `blocks/` |
| 5.8 guest + Proxmox drift, accept/revert/ignore once/rule | done | T-018, T-032, T-056, T-079 C |
| 5.8 raw-mode offer for unmappable changes | partly (Accept writes `overrides`; no explicit offer) | `target.json` `overrides` |
| 5.9 API, risk classes, token tiers, events, provenance | done; MCP/AI later | T-011, T-030 |
| §6 open questions | all answered | ARCHITECTURE §1–9, D-018 … D-041 |

### Known leftovers
| Item | Verdict | Reason |
|---|---|---|
| Two Images with the same base upload the repacked file twice | drop | pl-lead decided no card; one extra upload, no wrong result |
| 2 low `npm audit` (`deepmerge`, dev-only) | later | not in `web/dist`; take the fix with the next dependency refresh |
| GO-2026-5932 (module-only) | later | no call path; bump with the next Go dependency update |
| T-084 thin-pool note not shown in a real build Plan | slice 3 (C10) | one Image build Plan in the host run, a few minutes |
| Web mutation audit; Content-Length == cap boundary test | slice 3 (C8) | cheap, parallel, closes QA debt before new web work |
| Ubuntu 26.04 limit (D-192, D-220, D-223) | later, keep documented | workaround exists: Plumb-built Ubuntu Image or DHCP |
| Issues 14/15 on the test controller; stale worktrees (~139) | housekeeping, pl-lead, no card | no product change |

### Child cards (numbers from pl-lead)
| # | Title | Owner | Size | Depends on | Parallel |
|---|---|---|---|---|---|
| C1 | Contract: `update` command, agent version in Target status, version gate, schemas of the 4 resources | pl-architect | M | – | first |
| C2 | Agent self-update (download over the pinned link, SHA-256, self-test, atomic swap, keep `.prev`, restart) | pl-backend | L | C1 | with C4, C6 |
| C3 | Controller: agent version status, "update agent" Plan step, version gate on Plans | pl-backend | M | C1, C2 | after C2 |
| C4 | Agent resources `apt_repository`, `unit`, `sysctl`, `command` + guest tests | pl-reviewer as implementer, or pl-backend | L | C1 | with C2 |
| C5 | Starter Blocks `ssh-hardening`, `common-tools`, `shell`, `docker-compose` + guest tests | pl-codex-fix (YAML, small) | M | C4 | – |
| C6 | Web: Blocks screen (list, view, new, edit as YAML, save as proposal) | pl-frontend | L | C1 (uses existing proposal API) | from the start |
| C7 | Web: agent version and "update agent" on the Target view; gate message | pl-frontend | S | C3 | after C6 |
| C8 | QA debt: web mutation audit; Content-Length == cap test | pl-codex-qa | S | – | now |
| C9 | QA slice 3: tests against C2–C7; mutation audit of the gate and the update check | pl-codex-qa | M | each card | per card |
| C10 | Real-host acceptance: update an old agent in a VM and an LXC; new resources and `docker-compose` in both; T-084 note in a build Plan | pl-backend | M | C2–C7 | last |

### Risks and the real test host
- **Agent update can lose a guest's agent.** An LXC has no exec API, so recovery is re-enroll or re-create. Mitigation: SHA-256 check, `plumb-agent self-test` before the swap, `.prev` kept, a failed restart fails the step. CI guest tests cover a broken binary; C10 proves it on the host.
- **Docker in an unprivileged LXC with `nesting=1`** may need `keyctl`, which the token cannot set (H10). Only the host shows it (C10). Fallback: Question 4.
- **`apt_repository` key trust:** the key is inline text in the Block, never downloaded at apply.
- **`command`** has no drift and can do anything: human-only Plans, risk `disruptive`, a `creates` path so it runs once.
- **`sysctl` in an LXC** is not allowed: the resource declares `vm` only and the UI hides it.
- Host needed only for C10. C2–C5 run in the CI guest tests (Debian 13, Ubuntu 26.04).

### New decisions (proposed)
- D-256 · proposed · Slice 3 scope and order as above; slice 4 as above.
- D-257 · proposed · Agent update is a Plan step per Target (risk `reversible`); `system` proposes it when the agent version differs from the controller's; a person or a token approves. Nothing updates silently.
- D-258 · proposed · Update mechanism: command `update` with URL and SHA-256 over the pinned CA link; the agent checks the hash, runs the new binary's self-test, renames it over its own, keeps `.prev`, exits for a systemd restart. The step passes only when a `hello` with the new version arrives within 2 min.
- D-259 · proposed · Version gate: a Plan that sends a resource type or attribute the Target's agent version does not know is refused with "update the agent first". Closes the D-169 gap.
- D-260 · proposed · New resource types: `apt_repository` (deb822 file + inline key), `unit` (unit file + daemon-reload; timers are units), `sysctl` (vm only), `command` (no drift, `creates` guard, every Plan with it is human-only). `network` and `firewall` wait for slice 4 (need a safe apply with rollback).
- D-261 · proposed · Blocks screen edits the Block as YAML text, checked against `schema/block.json` in the browser and the server; save is a normal proposal, the Plan lists every Target the change touches.
- D-262 · proposed · `docker-compose` Block: Docker's apt repo with the key pinned in the Block; compose file as a `file` resource; a `unit` runs `docker compose up -d`. Supports `vm`; `lxc` only if C10 passes without new rights.

## Result (owner, max 8 lines)
branch · commit · tests · notes

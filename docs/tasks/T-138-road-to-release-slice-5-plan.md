# T-138 – Road to the release and plan of slice 5

> **For pl-lead – USER points (ask in one message now; details and recommendations: `docs/plans/ROADMAP.md` §1):**
> U1 SSH repair: Plumb repair key in new guests **(rec.)** vs. `VM.GuestAgent.Unrestricted` (slice 6) ·
> U2 adoption only of guests a person puts into pool `plumb` **(rec., no right)** vs. `Pool.Allocate` + `VM.Allocate` outside the pool (slice 6) ·
> U3 `SDN.Use` per more bridge / VLAN tag, and one isolated bridge on `pve-dev` for the proof (slice 7) ·
> U4 guest backups by Plumb: `VM.Backup` on the pool + `Datastore.AllocateSpace`/`Audit` on one backup storage **(rec.)** vs. a read-only view (slice 7) ·
> U5 AI help only through the user's own AI client via MCP **(rec.)** vs. an AI API key in Plumb (slice 8) ·
> U6 release steps (slice 9) · U7 repair run of the user's own install for the new ACLs (at the release).
> Slice 5 needs none of them and no new token right.

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** L (plan only, no code)   · **Depends on:** –

## Goal
The user decided (D-326): no release until every open point of `docs/BRIEF.md` §5/§6 is done, and the team plans and builds slice after slice on its own. We need (1) one list of everything that is still open and its order in slices, (2) the full plan of slice 5.

## Context (read only these)
- `docs/DECISIONS.md` D-326 and `AGENTS.md` "Autonomy and release"
- `docs/BRIEF.md` §5, §6 · `docs/STATUS.md` (rows T-107, "Open for the user") · `docs/ARCHITECTURE.md`
- `docs/tasks/T-107-*.md` as the model for a slice plan
- Prep cards run in parallel and write to `docs/plans/` on their own branches: T-139 (host facts), T-140 (risks), T-141 (web gaps), T-142 (code gaps). Use what is there; do not wait for them. `pl-lead` tells you when one lands.

## Acceptance criteria
- [ ] `docs/plans/ROADMAP.md`: every open point of BRIEF §5/§6 (at least: user management, `network` + more NICs, adoption of existing guests, SSH repair fallback, signed image checksums, MCP / AI help, guest backups, export/import, history rollback, the missing resources and starter Blocks, scheduled image rebuilds — check each against the code, do not trust this list), each with: state today, slice number, size, risks, and a mark **USER** if it needs a new token right or ACL, a public step, or anything outside `pve-dev`.
- [ ] Order: what unblocks most comes first; the USER points are collected early so that `pl-lead` can ask them in one message, long before the slice that needs them.
- [ ] Slice 5 plan in this card ("Plan"): scope, contracts, child cards with owner, size, dependencies and file ownership (cards that can run in parallel must not share files), migration numbers (next: 0017), acceptance on the real host.
- [ ] Plan questions: decide them with your recommendation and write them as decisions (numbers D-327 … D-346) to `docs/decisions/T-138.md`. Only USER points stay open; list them at the top of the card for `pl-lead`.
- [ ] PR to `main` (docs only); `pl-reviewer` checks it; report to `pl-lead`.

## Out of scope
- Code. Child cards are written by `pl-lead` from your plan.
- Any release step.

## Plan (architect)

Inputs used: prep files of T-139 (host facts), T-140 (risks), T-141 (web gaps), T-142 (code gaps). Road to the release: `docs/plans/ROADMAP.md`. Decisions: `docs/decisions/T-138.md` (D-327 … D-341).

### Slice 5 = "People, network, trusted images" (D-327)
1. **Starter catalogue** (D-328) – first, a product bug: no non-test code imports `blocks`, so a fresh install has no Blocks (T-141 #9). `GET /catalog/blocks`; the web adds or updates a Block by a normal proposal.
2. **User management** (D-329, D-330): roles `read` < `plan` < `apply` < `admin` on the scale of the token tiers; create with a one-time password, role change, disable (no delete, names never reused), last-admin rule in one transaction, sessions and tokens end with the role, login limit per (name, client) that always ends.
3. **Copy rule for Issue values** (D-331): a Plan that sets the observed value of an open Issue is human-only (`accept-guest-data`). Closes the D-323 limit for every client, also MCP later.
4. **`network`** (VM: extra addresses, routes, name servers, search; D-332) on a **generic safe apply** (D-333); starter Block `network`.
5. **`group`** resource (D-334).
6. **Signed checksums** (D-335, D-336): `verify` on an Image, keys pinned in the binary, "Refresh base", scheduled builds take a new signed pin by `system`, never a downgrade; Debian (no signature exists) stays a human refresh.
7. **Web gaps without new API** (D-338): delete Target/Block/Image, plan list and drop, run list, add a comment, compare two revisions, Issue filter by Target.
8. npm audit refresh (D-341), QA, real-host acceptance.

**Token rights:** none new. Catalogue, users, copy rule, `group`, web: inside Plumb. `network`: the agent in the guest. Signed checksums: the controller fetches over its existing egress (T-139 §5; `ctfetch` downloads today).

### Contracts (C1a, C1b; pl-architect writes them into `api/openapi.yaml`, the schemas and ARCHITECTURE §17)
- `GET /catalog/blocks` (read): `[{name, title, description, sha256, yaml, stored: none|same|differs}]`.
- `GET /users` (admin): `[{name, role, disabled, must_change_password, created_at, created_by, last_login_at}]`; `POST /users` (admin session) `{name, role}` → 201 `{name, password}` (no-store); `PATCH /users/{name}` (admin session) `{role?, disabled?}` → 409 `own-user` | `last-admin`; `GET /session` gets `role`. Events `user.created`, `user.changed`. Errors keep the D-325 rule (no value echo).
- Route table: one `access` per operation for tokens and roles (`internal/api/api.go` has it for tokens); `admin` = `sessionOnly` + role `admin`; a session with role `apply` approves up to `disruptive`.
- Plan reason `accept-guest-data` gains the D-331 trigger; `Plan.human_only_reasons` text names the Issue id.
- `network.json`, `group.json` (D-332, D-334); capability names `resource:network`, `resource:group` (agent version gate, as slice 3).
- `image.json`: `verify: {source: ubuntu-cloud | proxmox-aplinfo | debian-https}`; `POST /images/{name}/refresh` (plan) → Plan that changes only the pin, or 200 `unchanged`; Plan shows `{source, key_fingerprint, old, new}`; origin `system` for the schedule.
- C1b waits for the netplan probe (C5) and fixes file match, renderer and the live observe of `network`.

### Child cards (numbers from pl-lead)
| # | Title | Owner | Reviewer | Size | Depends on | Files it owns (parallel cards share none) |
|---|---|---|---|---|---|---|
| C1a | Contract: catalogue, users and roles, copy rule, `verify` + refresh | pl-architect | pl-reviewer | M | – | `api/openapi.yaml`, `internal/state/schema/image.json`, `docs/ARCHITECTURE.md` (§17 new, §7) |
| C1b | Contract: `network`, `group`, generic safe apply | pl-architect | pl-reviewer | S | C5, after C1a (same files) | `internal/resource/schema/{network,group}.json`, `docs/ARCHITECTURE.md` §17 |
| C2 | Catalogue API (`blocks.FS` into `serve`) + test that every catalogue Block passes the server check | pl-backend | pl-reviewer | S | C1a | `internal/api/catalog*.go`, one route line in `internal/api/api.go` (**merges before C3 touches `api.go`**) |
| C3 | Users and roles: migration **0017**, store, handlers, role in the route table, sessions/tokens on role change, last-admin transaction, login limit | **pl-reviewer as implementer** | **pl-architect** | L | C1a; `api.go` after C2 | `internal/store/migrations/0017_*.sql`, `internal/store/user*.go`, `internal/api/{api,session,users}*.go` |
| C4 | Copy rule D-331 in the Plan | pl-codex-fix (to pl-reviewer if > ~150 lines) | pl-reviewer | S | C1a | `internal/plan/humanonly*.go` |
| C5 | Netplan probe in CI guests (Debian 13, Ubuntu 26.04 cloud images): netplan present, renderer, `50-cloud-init.yaml` match, link drop of `netplan apply` on the agent TCP link | pl-codex-qa | pl-architect | S | – (**starts now**) | `test/guest/netplan_probe_test.go`, `docs/plans/netplan-probe.md` |
| C6 | Generic safe apply (firewall moved onto it, its tests unchanged) + resource `network` + CI guest tests incl. a forced rollback | pl-backend | pl-reviewer | L | C1b | `internal/agent/safeapply*.go`, `internal/resource/{network,firewall,firewall_guest}*.go`, registry line in `internal/resource/{resource,capability}.go`, `internal/agent/guest_network_test.go` |
| C7 | Resource `group` + guest test | pl-codex-fix | pl-reviewer | S | C1b, **after C6 merged** (registry files) | `internal/resource/group*.go`, registry lines, `test/guest/group_test.go` |
| C8 | Signed checksums: package `internal/trust` (fetch, verify, parse; pinned keys; fuzz), refresh proposal, schedule hook, VM pin `sha512` for Debian if C1a says so | **pl-architect as implementer** | pl-reviewer | L | C1a; its route line after C3 | `internal/trust/**`, `internal/plan/{schedule,image}*.go`, `internal/api/image_refresh*.go`, `go.mod`, `go.sum` |
| C9a | Web gaps without new API (D-338 list) | pl-frontend | pl-reviewer | M | – (**starts now**) | `web/src/screens/{Targets,TargetForm,Blocks,BlockView,ImageView,PlanView,RunView,IssueView,Issues,History,RevisionView}.svelte`, new `Plans.svelte`, `Runs.svelte`, `web/e2e/*gaps*` |
| C9b | Web: catalogue on the Blocks page, users page, role-aware buttons, Image `verify` + "Refresh base" | pl-frontend | pl-reviewer | M | C1a (mock), C2, C3, C8; after C9a (same screens) | `web/src/screens/{Settings,Users,Blocks,ImageForm,ImageView}.svelte`, `web/src/lib/session*.ts`, e2e |
| C10 | npm audit refresh (`deepmerge`) | pl-codex-fix | pl-reviewer | S | – | `web/package.json`, `web/package-lock.json` (C9 does not touch them) |
| C11 | Starter Block `network` (+ render test) | pl-frontend | pl-reviewer | S | C6 | `blocks/network.yaml`, `blocks/network_test.go` |
| C12 | QA: role × operation matrix test read from `api/openapi.yaml` (D-329); parallel demote of the two last admins; fuzz + mutation audit of `internal/trust`; mutation audit of the copy rule and of the network rollback | pl-codex-qa | – | M | per card | `*_qa_test.go` next to each card's files (after that card merged) |
| C13 | Real-host acceptance (below) | pl-backend | pl-reviewer | M | C2–C11 | `docs/tasks/T-NNN.log.md` |
| C14 | README: users, catalogue, `network`, signed images | pl-frontend | pl-reviewer | S | C13 | `README.md` |

Parallel from the start: C1a, C5, C9a, C10. Then C2 → C3 (critical path), with C4, C8, C6 (after C1b). Codex cards are narrow (C4, C5, C7, C10 ≤ ~150 lines each). pl-reviewer implements C3, so pl-architect reviews it; pl-architect implements C8, so pl-reviewer reviews it.

### Risks
- **Roles and the human-only rules mix up.** Mitigation: role is an extra check after "human session"; the OpenAPI matrix test (C12) fails for an operation without a rule; D-330 closes the T-140 §1 cases one by one, each with a test.
- **Users tokens:** a demoted admin's tokens are disabled, not deleted; re-enable is the existing `POST /tokens/{id}/enable`.
- **`network` cuts the link.** Safe apply rolls back after 90 s (D-293); the schema refuses the main address and a default route. The probe (C5) must show that `netplan apply` keeps or quickly restores the agent connection; if not, C1b stops and pl-lead gets a short note before C6 starts.
- **Safe-apply refactor breaks `firewall`.** C6 moves firewall onto the generic code with its slice 4 tests unchanged (incl. the CI guest rollback test) and the mutation set of T-120 re-run.
- **Signed checksums:** wrong or rotated vendor key = every refresh fails with a clear text, the old pin keeps working (builds do not stop). Fingerprints checked from two sources in C1a. Debian has no signature (T-139 §5): its refresh stays human; the UI says "not signed by Debian".
- **Catalogue overwrite:** "Update from the catalogue" is a Plan with a diff; never automatic.
- Migration 0017 only (D-337). Host: one agent at a time (C13); all other cards prove in CI (fake PVE, CI guest tests).

### Real-host acceptance (C13, `pve-dev`, controller on the latest main)
1. Upgrade path: migration 0017 on the live DB; the existing user is `admin`; no Issue appears.
2. Catalogue: an empty Block list fills by "Add" Plans; a changed stored `motd` shows "differs" and its update Plan shows the diff.
3. Users: an admin makes `ops` (`apply`) and `viewer` (`read`); `viewer` cannot propose; `ops` approves a `reversible` Plan, not a `destructive` one, not a user change; demote of the last admin → 409; disabling `ops` ends its session (401) and disables its token.
4. Copy rule: a `plan` token proposes the observed value of an open Issue → the Plan is human-only, an `apply` token gets 403.
5. `network` on a Debian 13 and an Ubuntu 26.04 VM: extra address and route from the documentation ranges (192.0.2.0/24, 198.51.100.0/24, no LAN address), name server change; `ip addr del` by hand → Issue → Revert; a route that cuts the controller (controller /32 via an on-link documentation address) rolls back by itself within 120 s; `network` on an LXC is refused at save.
6. `group` on VM and LXC: create with gid, a user joins it, `groupdel` by hand → Issue.
7. Signed checksums: an Ubuntu 26.04 Image with `verify: ubuntu-cloud` and a Debian 13 LXC Image with `proxmox-aplinfo`: "Refresh base" shows source, fingerprint, old → new; a scheduled build with an out-of-date pin is refreshed and approved by `system` and the clone enrolls; a Debian VM Image with `debian-https` needs a person.
8. Leak hunt as in T-037 (one-time passwords, tokens): zero hits in logs, events, export. Host clean at the end (only LXC 100).

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-172 – Plan of slice 6

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** L (plan only, no code)   · **Depends on:** –

## Goal
Slice 5 is built and accepted on the host (T-158); only small PRs are left. By D-326 the team plans the next slice at once. The user has **not** answered U1 … U5 yet (asked 2026-11-13). So slice 6 must be cut so that work can start without those answers.

## Context (read only these)
- `docs/plans/ROADMAP.md` (rows 9–20, §1 questions) · `docs/DECISIONS.md` D-326 … D-359 · `docs/tasks/T-158.log.md`
- `docs/tasks/T-138-road-to-release-slice-5-plan.md` as the model

## Acceptance criteria
- [x] Update `docs/plans/ROADMAP.md`: slice 5 rows done; new open points from slice 5 added, each with a slice:
  - per-agent rate limit for drift reports (review of #256; proposal there),
  - immediate drift detection for live `network` / `firewall` hand changes (today only at the 15 min full check; BRIEF §5.8 wants "immediately") — netlink / nft monitor, or say why not,
  - `firewall` set to absent after a hand change is free for a token (review of #264, low),
  - `network`: validate the MAC, compare `permaddr` (review of #268, low),
  - F5 of T-158: a way to prove the `system` approval of a newer signed pin end to end (e.g. a fake vendor source in a CI e2e),
  - anything else you find open in the review comments of #241 … #271 (sweep them; list what you dropped and why).
- [x] Slice 6 plan in this card: **part A** needs no user answer (roadmap rows 9, 10, 13 and the points above; consider pulling row 16 "MCP server" forward if U5 only decides row 17) and starts at once; **part B** (rows 11 SSH repair, 12 adoption) is planned with both answers of U1/U2 in mind and starts only after the user's answer. Never assume an answer to a U question (D-326 (4)).
- [x] Same form as T-138: contracts, child cards with owner, size, dependencies, files (parallel cards share none; lesson of slice 5: name the shared files `api.go`, `propose.go`, `plan.go`, `serve.go`, `openapi.yaml` marks, registry files and give the order), migration numbers (next: 0020), risks, real-host acceptance.
- [x] Lessons of slice 5 as rules in the plan: every new resource type gets the opaque/guest-value classification test (D-359); a host step for each human-only rule with a token; CI guest tests only in the VM job when they need the real image.
- [x] Decisions with your recommendation in `docs/decisions/T-172.md` (ask `pl-lead` for numbers). PR to `main` (docs only), pl-reviewer checks, report.

## Out of scope
- Code. Any release step.

## Plan (architect)

> **For pl-lead:** part A starts now (cards A1, A4b, A7, A9, A10, A11 have no dependency). Part B waits for U1 and U2; no new user question. Do now, no card: shred `~/.config/plump/t158-secrets.json` (#265). New team rule (D-375): PR screenshots only from the mock.

Inputs: ROADMAP rows 9–27, D-326 … D-359, `T-158.log.md` (F1 … F5), review sweep of #241 … #271 (D-375), code on `main` a7c5962. Decisions: `docs/decisions/T-172.md` (D-360 … D-375).

### Scope (D-360)
**Part A – no user answer, starts now**
1. **Settings** (row 9, D-361): backup time and keep editable (a stored setting wins over env), read-only Proxmox view with the ACL check.
2. **Accept to a Block parameter** (row 10, D-362): strict inverse of `prefix{{ .p }}suffix`, proven by a re-render that changes nothing else; else raw as today.
3. **Object parameters, YAML mode** (row 13, D-363): web only.
4. **MCP server** (row 16, D-364) and the **per-token rate limit** (row 26, D-365).
5. **Drift report rate limit** (row 21, D-366): back-pressure, never a drop.
6. **Live drift of `ip` / `nft`** (row 22, D-367): netlink as a trigger only.
7. **Firewall removal is human-only** (row 23, D-368), **`network` MAC checks** (row 24, D-370), **F5 proof in CI** (row 25, D-369), **review leftovers** (row 27, D-375).

**Part B – starts after the user's answer to U1 / U2** (planned for every answer below)
8. **SSH repair fallback** (row 11): repair core (D-372) + the transport that U1 picks.
9. **Adoption** (row 12): D-373, + the move step only if U2 says (b).

**Token rights:** part A none. Part B: none for U1 (a) and U2 (a); U1 (b)/(c) and U2 (b) need new ACL lines (installer change + U7 for the user's own install).

### Contracts
**A1 (pl-architect: `api/openapi.yaml`, ARCH §18 new "Slice 6", regenerated `web/src/lib/api.gen.ts`)**
- `GET /settings` (read) → `{backup: {time, keep, source}, proxmox: {node, pool, storages: {images, snippets, backup}, api: {host, port, tls_sha256}, version, token_user, last_ok_at, acl: ok | missing | unknown, acl_lines: [{path, role, ok}]}}`; `PATCH /settings` (session, admin) `{backup?: {time?, keep?}}` → 200 with the new settings; 422 by the D-325 rule (no value echo). Event `settings.changed`.
- `Issue.accept_mode: param | raw | none`, `Issue.accept_reason` (text when raw), `Issue.accept_params: [{use, param}]`; `Issue.raw` (D-301) stays and equals `accept_mode == raw`.
- `429` `rate-limited` with `Retry-After` on every proposal route, on `POST /issues/{id}/comments` and on every token `GET` except the event stream; mark `x-plumb-rate: proposal | comment | read` (D-365, read bucket after #272 F3), read by a contract test so no route is missed. Link: 429 on the drift report route.
- Plan reason `firewall-off` (D-368).
- Agent hello: `live_watch: {network: bool, firewall: bool}` (D-367); `TargetView.agent.live_watch`.
- ARCH §17: the known limit of partial port-range copies (#248 L2). ARCH §18: MCP tool list and the data-field rule (D-364).
- Check before writing: what agents of the last two releases do on 429 (D-366); whether the token may read its own `/access/permissions` (PVE source, or one read-only call on `pve-dev` by the host card owner); the MCP Go SDK license and age (D-364).

**B1 (pl-architect, after the answer).** D-372 and D-373 hold for every answer. B1 writes the routes, the repair bundle format and the transport that U1 picks:
- **U1 (a) SSH key.** One ed25519 key per controller, made in Go (`golang.org/x/crypto/ssh`; LXC 100 has no `ssh-keygen`); the private part in the secret store under a reserved name (never listed, never in an export, in the backup only encrypted by the master key). Line `restrict,from="<controller addresses>",command="<fixed POSIX sh, inside the line, needs no file>" ssh-ed25519 … plumb-repair` in `/root/.ssh/authorized_keys`, written by the agent at enrollment and at every start; so every guest whose agent lived once has it ("new guests at create" = the agent's first start; no driver change). The agent reports `repair_ready` (`line`; `sshd` from `sshd -T`: root login with forced commands allowed; host key fingerprints) over the pinned link. The controller pins the host keys per Target (migration 0021) and refuses an unknown or changed key; a person may accept a shown new fingerprint (human-only). `ssh-hardening` gets `forced-commands-only` as a value of `permit_root_login`; with `no` the Target shows "repair fallback off". A `firewall` Plan warns when `ssh_sources` does not hold the controller address. Rotation `POST /settings/repair-key/rotate` (admin session): live agents write the new line and drop the old one; the old key stays valid until every agent confirmed or 7 days passed. Unbind and Target delete: the agent removes the line first. LXC with sshd: the same; LXC without sshd: no fallback (UI text).
- **U1 (b) Proxmox guest agent.** VM only. New ACL `VM.GuestAgent.Unrestricted` on `/pool/plumb` (installer change; U7 for the user's own install). The driver sets `agent: 1` at create (existing VMs: a Plan with a restart); VM Image builds install `qemu-guest-agent`. Repair calls `agent/exec` with one fixed command that fetches the bundle from the controller over HTTPS with the CA pin and a one-time URL, then `agent/exec-status` with a bounded wait. No host key needed. LXC: no fallback.
- **U1 (c) both.** Guest agent for VMs where it answers, SSH otherwise (LXC, VMs without `qemu-guest-agent`). Both transport cards are built.
- **U2 (a).** D-373 only.
- **U2 (b).** In addition `POST /guests/{vmid}/move` (tier `plan`) → Plan, human-only reason `adopt`, risk `disruptive`, that puts the guest into pool `plumb`. New ACL lines `Pool.Allocate` on `/pool/plumb` and `VM.Allocate` + `VM.Audit` on the paths of the guests outside the pool that the person names at install (never on `/`). Installer change + U7.

### Child cards (numbers from pl-lead)
Shared files and their order (D-371 (4)): `api/openapi.yaml`, `web/src/lib/api.gen.ts` → A1 only (B1 in part B) · `internal/api/api.go` → A2, then A6a (part B: B2) · `internal/plan/humanonly.go`, `propose.go` → A8 (part B: B2 after A8, then B4) · `internal/plan/plan.go` → none in part A · `cmd/plumb/serve.go` → A2 · `cmd/plumb/main.go` → A5 (one subcommand line) · registry `internal/resource/{resource,capability}.go` → none in part A · `internal/link/link.go` → A6b · `internal/agent/drift.go` → A7 (A6b changes no agent source: today's agent already keeps unsent drift, #272 F1) · `internal/wire/protocol.go` → A7 (one hello field) · `go.mod`/`go.sum` → A5 (part B: B3a after it) · `web/src/lib/{router,client}.ts`, `web/src/App.svelte` → A4a only (A4b uses none) · `install/install.sh` → part B only (B3b, then B4 for U2 (b)).

**Part A**
| # | Title | Owner | Reviewer | Size | Depends on | Files it owns |
|---|---|---|---|---|---|---|
| A1 | Contract part A (above) | pl-architect | pl-reviewer | M | – (**now**) | `api/openapi.yaml`, `web/src/lib/api.gen.ts`, `docs/ARCHITECTURE.md` §17 (one sentence), §18 |
| A2 | Settings server: migration **0020**, store, `GET`/`PATCH /settings`, schedule reload without restart, Proxmox view + ACL check; `proxmox` only for a human session, `null` for a token (review n2 of #276) | pl-backend | pl-reviewer | M | A1 | `internal/store/migrations/0020_setting.sql`, `internal/store/setting*.go`, `internal/api/settings*.go`, routes in `api.go` (**first**), `cmd/plumb/serve.go`, `internal/backup/service.go`, `internal/driver/proxmox/acl*.go` (new, read-only calls) |
| A3 | Accept to a Block parameter (D-362): template inverse, re-render proof, Issue fields | **pl-architect as implementer** | pl-reviewer | L | A1 | `internal/state/template_inverse*.go`, `internal/issue/issue.go` (`acceptDoc`, `AcceptRefused`), `internal/issue/accept_param*.go`, `internal/api/views.go` (Issue view fields) |
| A4a | Web: settings tab "General" (backup edit, Proxmox view); Issue view: "Accept into parameter `p` of `use`" or "Accept as raw" with the reason; rate-limit message; `live_watch` note on the Target; Plan screen: the reason of a token-made Plan below the changes, tagged "written by API token …" (#272 F4), and a hint next to "Approve and apply" while document changes are still closed (#269 n2) | pl-frontend | pl-reviewer | M | A1 (mock), A2, A3 | `web/src/screens/{Settings,Backups,IssueView,TargetAgent,PlanView}.svelte`, new `web/src/screens/General.svelte`, `web/src/lib/{router,client,api}.ts`, `web/src/lib/mock/**`, `web/e2e/*settings*` |
| A4b | Web: nested forms for object / array-of-object parameters, YAML mode for Target and Image (D-363) | pl-frontend | pl-reviewer | M | – (**now**) | `web/src/lib/{schemaForm,yaml}.ts`, `web/src/ui/SchemaFields.svelte` (+ new nested parts in `web/src/ui/`), `web/src/screens/{TargetForm,ImageForm}.svelte`, `web/e2e/*yaml*` |
| A5 | `plumb mcp` (D-364) + CI test with an MCP client against `serve --dev`; stdio `MaxLineLength` set explicitly (4 MiB, review n1 of #276) | **pl-reviewer as implementer** | **pl-architect** | M | A1 | `internal/mcp/**`, `cmd/plumb/mcp.go`, one line in `cmd/plumb/main.go`, `go.mod`, `go.sum`, `test/mcp/**` |
| A6a | Per-token rate limit (D-365): middleware by the `x-plumb-rate` mark, 429, event | pl-codex-fix | pl-reviewer | S | A1; `api.go` after A2 | `internal/api/tokenrate*.go`, one middleware line in `internal/api/api.go` |
| A6b | Per-agent drift report limit (D-366): bucket in the link; a test that a real agent keeps and resends its drift after a 429 (no agent code change: #272 F1, F2) | pl-backend | pl-reviewer | S | A1 | `internal/link/link.go`, `internal/link/driftrate*.go`, new test file `internal/agent/drift_busy_test.go` (A6b changes no agent source file; `drift.go` stays with A7) |
| A7 | Live drift of `ip` / `nft` (D-367): netlink subscribe, trigger only, debounce, `live_watch` in hello; fuzz of the header check; CI guest test in the **VM job** (address del, route add, `nft add rule` → report within 10 s) | pl-backend | pl-reviewer | M | – (**now**; hello field after A1) | `internal/agent/netwatch*.go`, `internal/agent/drift.go`, `internal/agent/guest_netwatch_test.go`, `internal/wire/protocol.go` (one field) |
| A8 | `firewall-off` (D-368) | pl-codex-fix | pl-reviewer | S | A1 | `internal/plan/humanonly.go`, `internal/plan/firewall_off_test.go` |
| A9 | `network` MAC check + `permaddr` (D-370) | pl-codex-fix | pl-reviewer | S | – (**now**) | `internal/resource/network_apply.go`, `internal/resource/network_mac_test.go` |
| A10 | F5 proof (D-369): `plumbtest` test key, local signed lists (`httptest`), schedule → refresh → `system` approval → build Plan at engine level, release-key test | pl-codex-qa | pl-architect | S | – (**now**) | `internal/trust/testkey_plumbtest.go`, `internal/trust/testdata/f5/**`, `internal/plan/pinrefresh_e2e_test.go`, `internal/trust/release_keys_test.go` |
| A11 | Review leftovers (D-375): netplan probe out of CI; tests #256 L1, L3 and the two-report chunk test of #248 | pl-codex-fix | pl-reviewer | S | – (**now**) | `test/guest/lxd.sh`, `test/guest/netplan_probe_test.go` (delete; `docs/plans/netplan-probe.md` stays as the fact record), `internal/store/guestvalue_qa_test.go` |
| A12 | QA per card: mutants of A3 (a parameter that also feeds another resource → raw; drop the re-render → a test fails), 429 paths (token, agent; coalescing keeps the latest; 2 min block then the latest drift arrives), MCP tool list = the fixed set (an `apply` token gets no approve tool), `firewall-off` by a Block delete, `PATCH /settings` by a token → 403 | pl-codex-qa | – | M | per card | `*_qa_test.go` next to each card's files, after that card merged |
| A13 | Real-host acceptance part A (below) | pl-backend | pl-reviewer | M | A2–A11 | `docs/tasks/T-NNN.log.md` |
| A14 | README: settings, Accept to a parameter, MCP setup (`plan` token), one line on the copy rule for token users (#270 n2) | pl-frontend | pl-reviewer | S | A13 | `README.md` |
| A15 | Target form writes only what the person set (D-376, card T-196; found in T-177): a parameter equal to the Block default is left out of `with`, an empty `overrides` is not written; the server already fills defaults at render (`internal/state/render.go`, `withDefaults`). Existing documents are **not** rewritten: the form shows a parameter that is stored with the default value as "fixed to the current default" with a per-parameter action "Follow the Block default" (removes it, by the normal proposal); a save keeps every stored value as it is. Test: a Block default change reaches a Target made by the form and does not reach one that fixed the value | pl-frontend | pl-reviewer | S | A4b (same files) | `web/src/screens/TargetForm.svelte`, `web/src/lib/schemaForm.ts`, `web/src/ui/SchemaFields.svelte`, `web/e2e/*defaults*` |

Parallel at once: A1, A4b, A7, A9, A10, A11. A15 after A4b. After A1: A2 → A6a (`api.go`); A3, A5, A6b, A8 in parallel (A6b and A7 share no file). A4a after A2 and A3. Codex cards (A6a, A8, A9, A10, A11) stay at ≤ ~150 lines each. Cross-check: Claude code is tested by Codex (A12); A5 is built by pl-reviewer, so pl-architect reviews it; A3 is built by pl-architect, so pl-reviewer reviews it.

**Part B** (after the answer; B1 picks the rows that apply)
| # | Title | Owner | Reviewer | Size | Depends on | Files it owns |
|---|---|---|---|---|---|---|
| B1 | Contract part B for the given answers | pl-architect | pl-reviewer | M | U1, U2 answered; A1 merged | `api/openapi.yaml`, `web/src/lib/api.gen.ts`, ARCH §18 |
| B2 | Repair core (D-372): Plan origin `repair`, reason `repair`, bundle, one-time token, revoke of the old credential, output limits | pl-backend | pl-reviewer | L | B1; `humanonly.go`/`propose.go` after A8 | `internal/plan/repair*.go`, `internal/plan/humanonly.go`, `internal/plan/propose.go`, `internal/api/repair*.go`, route line in `api.go` |
| B3a | **Only U1 (a)/(c):** SSH transport: key, line, `repair_ready`, host-key pins (migration **0021**), rotation, `ssh-hardening` value, firewall warning, line removal on unbind; CI guest test (VM job): dead agent repaired; changed host key → refused | pl-backend | pl-reviewer | L | B2 | `internal/repair/ssh*.go`, `internal/agent/repairline*.go`, `internal/store/migrations/0021_*.sql`, `internal/store/hostkey*.go`, `blocks/ssh-hardening.yaml`, `go.mod`, `go.sum` |
| B3b | **Only U1 (b)/(c):** guest-agent transport, `agent: 1`, `qemu-guest-agent` in VM builds, installer ACL line | pl-reviewer (implementer) | pl-architect | L | B2; after B3a if both (`go.mod`) | `internal/repair/qga*.go`, `internal/driver/proxmox/agent_exec*.go`, `install/install.sh` (ACL), `internal/plan/build.go` (package step) |
| B4 | Adoption (D-373): list, adopt Plan, digest checks, redaction, warnings, install command, unbind on delete; **only U2 (b):** the move Plan and its installer ACL lines | pl-architect (implementer) | pl-reviewer | L | B1; `propose.go` after B2 | `internal/plan/adopt*.go`, `internal/api/adopt*.go`, `internal/driver/proxmox/adopt*.go`, `internal/state/schema/target.json` (`adopted`), `install/install.sh` only for U2 (b), after B3b |
| B5 | Web: repair action and state, adoption list and wizard, install command (shown once) | pl-frontend | pl-reviewer | M | B1 (mock), B2, B4 | `web/src/screens/{TargetAgent,Targets}.svelte`, new `Adopt.svelte`, `web/src/lib/{router,client}.ts`, e2e |
| B6 | QA: T-140 §2, §3 tests (no key bytes in an export, a log or an unencrypted backup; changed host key; `apply` token → 403; guest swapped between Plan and Run; `cipassword` value in no table, event or log; delete of an adopted Target → no Proxmox delete) | pl-codex-qa | – | M | per card | `*_qa_test.go` |
| B7 | Real-host acceptance part B (below) | pl-backend | pl-reviewer | M | B2–B5 | `docs/tasks/T-NNN.log.md` |

### Migrations (D-374)
0020 `setting` (A2) · 0021 host-key pins + repair key metadata (B3a, only for U1 (a)/(c)) · 0022 reserved for adoption (B4, only if a column is needed). pl-lead renumbers at the merge if needed (D-337).

### Rules from slice 5 (D-371)
1. Every new resource type is classified (value type or named opaque attributes); the D-359 test fails otherwise. Slice 6 plans none; the repair line is agent-internal. A card that adds a type adds its classification in the same PR.
2. Every human-only rule that a token could pass has a real-host step with a token (steps A13.2, .3, .5, .6; B7.1, .3).
3. CI guest tests that need the real cloud image run only in the VM job (A7, B3a).
4. Shared files: one owner at a time, in the order above. Contract files change only in A1 / B1. A card that adds an API operation updates `web/src/lib/roleHints.ts` in the same PR (pl-lead, T-173).
5. Push rule (memory "CI runner scarce"): push a PR branch only with complete, merge-needed changes.

### Risks
- **Accept to a parameter writes more than the person saw.** The re-render must leave every other resource and attribute equal (D-362); a parameter that feeds two resources goes raw. A12 mutants: no re-render, no "used nowhere else" check.
- **The template inverse is wrong in a corner case** (escapes, `{{-`, a default that the Target does not set). Only the plain field action maps; the Target gets the `with` entry also when it used the default; everything else is raw. It fails to raw, never to a wrong value.
- **A rate limit hides drift.** Back-pressure only. Fact (#272 F1, F2): today's agent maps 429 to `busyError` (`internal/agent/client.go:140`), keeps its credential and the link, sleeps `Retry-After` (at most 10 min), and does not mark the refused items sent (`sendDrift` returns no handled items on a temporary error; `markSent` in `drift.go`), so it sends the latest state per resource (`open` + `dirty`) again. No version gate is needed; A6b proves it with a test.
- **Netlink trigger storms** (DHCP renew, SLAAC, Docker bridges): debounce 2 s, one check per 5 s per type; the check is the same cheap read as today. In an LXC the nftables subscribe may fail: `live_watch: false`, the full check stays.
- **MCP prompt injection.** Guest text only as data fields; no approve tool; the server rules (D-331, D-359, human-only) stand behind it; the per-token limit stops loops. No HTTP mode.
- **Settings vs. installer.** A stored setting wins; the installer env lines stay defaults; the view shows the source, so a person sees why a repair run changed nothing.
- **`firewall-off` needs a person** also for a Block delete that holds a firewall; the Plan says why.
- **Part B waits.** If the answer comes late, part A fills the slice; B1 starts on the day of the answer. If U1 is (b)/(c) or U2 is (b), U7 (repair run of the user's own install) is needed before the user can use it; pl-lead tells the user at the merge.
- **SSH repair (U1 a/c):** a guest whose agent never started has no line, so no fallback (UI text); `PermitRootLogin no` or `ssh_sources` without the controller turn the fallback off (shown, not hidden). The key is root on every guest: secret store, no export, no log, rotation (T-140 §3).
- **Guest agent (U1 b/c):** the token is root in every pool VM; the stock Debian cloud image has no `qemu-guest-agent` (T-139 §4), so only VMs built after the change have it.
- **Adoption:** wrong guest by VMID reuse (D-320) → node + VMID + name + digest before each write; secrets of the Proxmox config redacted; an adopted guest is not sealed (UI text, D-340).

### Real-host acceptance
**A13 (part A, `pve-dev`, controller on the latest main)**
1. Upgrade: migration 0020 on the live DB; `GET /settings` shows `source: env`, the Proxmox view and `acl: ok` (or `unknown` with the reason of A1).
2. Settings: an admin sets the backup time to 2 minutes ahead; the backup runs then, without a restart; an installer `--dry-run` repair run keeps the stored value; `PATCH /settings` by an `apply` token → 403, by a user with role `apply` → 403.
3. Accept to a parameter: `motd` on a Debian 13 VM; a hand edit of `/etc/motd` → Issue with `accept_mode: param`; an `apply` token gets 403 (`accept-guest-data`); a person approves; the Target has `with.message`, no override; the Issue resolves. A test Block whose parameter feeds two files → `accept_mode: raw` with the reason.
4. Live drift: `ip addr del` of a `network` extra address and `nft add rule … dport 8081 accept` on a VM → each Issue within 10 s; on an LXC with `firewall`: Issue within 10 s, or `live_watch: false` shown and the full check opens it.
5. `firewall-off`: a `plan` token proposes `state: absent` for the firewall → `human_only: [firewall-off]`, `apply` token 403; the same by a Block delete; drop both.
6. MCP: on the dev VM, `plumb mcp` with a `plan` token against the controller: list the Issue of step 4, comment on it, propose a revert (a Plan, not approved); no approve tool in `tools/list`; the 31st proposal in one minute gets `rate-limited`.
7. `network` on Ubuntu 26.04 with the kept `ens18` still works (D-370 did not break T-167).
8. Leak hunt (as T-037) incl. MCP output and settings events. Host clean (only LXC 100).

**B7 (part B, steps by the answer)**
1. U1 (a)/(c): a Debian 13 VM and a Debian 13 LXC with sshd; break the agent (`systemctl mask`, credential removed); after 10 min "Repair" is offered; a `plan` token proposes, an `apply` token gets 403 (`repair`), a person approves; the agent is back with a new credential, the old one is refused. New host keys on the VM → repair refused until a person accepts the shown fingerprint. The line has `restrict`, `from=`, `command=`; rotation replaces it; unbind removes it.
2. U1 (b)/(c): the same on a VM with `qemu-guest-agent`; the token has exactly the old 7 ACL lines + the new one.
3. Adoption: a person makes a VM and an LXC in the Proxmox UI and puts them into pool `plumb`; both are in `GET /guests/unmanaged`; no `cipassword`/`sshkeys` value in the DB (grep); adopt by a person (token 403); a rename between Plan and Run → the Run refuses; after a new Plan the install command enrolls the agent; a Block applies; delete of the adopted Target only unbinds (the guest stays; removed by hand at the end). U2 (b): the move Plan for a guest outside the pool first.
4. Leak hunt incl. the repair key (no key bytes in logs, events, export; the backup holds it only encrypted). Host clean.

## Result (owner, max 8 lines)
branch · commit · tests · notes

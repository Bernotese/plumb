# Status

Product: **Plumb** – self-hosted infrastructure controller for a Proxmox homelab. Brief: `docs/BRIEF.md`.
(Naming: the product is "Plumb". The herdr workspace and local folder are still called `plump`.)

## Phase
First slice: one VM, from an image, configured by the agent, drift as an issue. Secret store is not part of the slice.
Controller, API and UI are on `main`. Try it: `make build && bin/plumb serve --dev --dev-demo` (simulated agents, no Proxmox).
The user merged PR #8, #5, #13 on 2026-11-10. The real guest tests run in the CI of `main` and are green (Debian 13, Ubuntu 26.04). The whole agent track of slice 1 is on `main` (#5, #8, #13, #21, #27). **Slice 1 is accepted without Proxmox** (T-015, PR #67: HTTP API + real agent). The agent side is proven as root in real Debian 13 and Ubuntu 26.04 guests in CI (resources, the whole agent loop, secret masking in real tool output). Open: everything on the real host. **Slice 1 is accepted on real Proxmox (2026-11-11).** **The Proxmox test host `pve-dev` is ready** (address: `~/.config/plump/testhost.env`, not in the repo; rules: AGENTS.md). pl-lead updated it on 2026-11-11 with the user's go: pve-manager 9.2.26, qemu-server 9.2.15, libpve-storage-perl 9.1.15, rebooted, no failed unit. Before the update the installer dry run refused the old packages and changed nothing (T-013 log). **Slice 2 part A is complete on `main`** (ARCHITECTURE §12, D-085 … D-158): secret store and secret parameters, token management, revision history with rollback, ignore actions, export/import, their UI, e2e tests, and the acceptance with leak hunt (zero hits). Part B (image builds, LXC) is designed and waits for the test host.

## Tasks
| Card | Owner | State |
|---|---|---|
| T-038 LXC targets (slice 2 part B, second) | child cards T-075 … T-083, T-085 | plan approved by the user (D-238). H10 done (PR #153): the token cannot make a privileged container, a dangerous feature, a host mount or a device. **On main:** T-075 contract (#154), T-076 `ctpack` (#156, incl. the CA pin of the first-boot download for VMs and LXC), T-077 installer (#157), T-078 agent (#161), T-080 engine (#159), T-081 web (#165), T-079 PR A driver lifecycle (#162) and PR B fetch + host recordings (#167). **Open:** T-079 PR C outside additions (PR #169, review + QA fixes open); T-085 Image status for `lxc` (pl-frontend, after PR C); T-082 LXC image builds; T-083 real-host acceptance. Test host: repair run to main `e295d94` done (`plumb-images` = `import,vztmpl`), clean. |
| T-084 token gets `Sys.Audit` (D-239, user decision) | pl-backend | step 1 done: only the ACL path `/` works (Proxmox checks the fixed path `/`). **Waits for the user: yes or no to `Sys.Audit` on `/` without propagation.** |

Order with the test host: real-host part of T-015 on the running install → `--remove` and a fresh install from main (D-180) (two static VM addresses from the user are in `~/.config/plump/testhost.env`) → slice 2 part B (T-039 image builds, then T-038 LXC).

## Done (2026-11-10 … 2026-11-12)
- 2026-11-12: retention error rule (PR #150, D-232): a driver error in retention fails the step and the Run and says that the new version is ready; a version kept on purpose (locked, linked clone, in use) is a note.
- 2026-11-12: T-065 rest gaps of the image build audits (PRs #146, #147, #149, test-only): build failure clean-up at every step, retention variants, events and version pins. Real host (PR #148): a stop of the service during a build takes 1.66 s, no SIGKILL, the stop's own clean-up removes the build guest; the host runs main.
- 2026-11-12: seal step order for Ubuntu (PR #143: the initrd no longer carries the build hostname or machine-id; proven on the host) and a product shutdown defect (PR #144: `serve` waited for nothing and closed the store while background work still used it; the first fix deadlocked once in the CI race job and was corrected before the merge: Stop cancels the Runs and starts a 2 min grace, `TimeoutStopSec=180`). Wording of the Ubuntu limit (PR #145, D-223).
- **T-039 (2026-11-11): image builds** (slice 2 part B, first half; D-197 … D-210, D-218, D-221 … D-223; migrations 0009, 0010). An Image can have a build recipe (Blocks without secrets), retention and a schedule. A build is a Run: build guest, Blocks, agent `seal` with a power-off handshake, template, version, retention. Only a person approves a recipe change; scheduled builds are approved by `system`. PRs: #110 plan, #115 contract, #121 state, #125 + #136 driver, #127 disk gate (T-074), #138 engine + schedule, #134 agent seal, #123 + #139 web. Every part reviewed and mutation-audited. **Real-host acceptance passed** for Debian 13 and Ubuntu 26.04 (T-066 log, PR #142): template scan clean (no credential, host keys, machine-id, logs, build values), Ubuntu static address 5 of 5 first boots, retention, schedule, 85 % gate. Known limits: no linked-clone sign on lvmthin (D-221); thin-pool metadata use unknown without Sys.Audit (the Plan says so).
- **T-067 (2026-11-11): the agent reports its addresses** (PRs #106 plan, #114 contract, #116 agent, #118 controller + migration 0008, #117 web, #129 host log; D-212 … D-217, D-218). `ips` stays the configured address; `reported_ips` is the guest's list, display only. A static Target whose address is not reported gets a `guest:address` Issue after 2 min; it closes by itself. Security fix on the way (#119): agents can no longer open or close Issues of `guest:*` resources. Real host: all checks pass. T-074 (#127, D-219): one disk gate for creates and builds, limit 85 % by default.
- **T-073 (2026-11-11): a failed guest create removes its clone** (PR #111, host log PR #112). Cause: the name of a fresh clone came from Proxmox's delayed pool list (no name for 2 … 8 s), so the cleanup skipped it. Real host: a too small disk is refused before the clone; a start failure leaves no VM. Not provokable on the host: a config or resize failure (covered by fake PVE tests). Same host run: **T-057 proven on the real host** (old credential 204 before the delete, 401 after). The controller on the test host runs main ac9594c.
- **T-057 (2026-11-11, security, MEDIUM): a Target delete revokes its agent** (PR #104, D-211). Before, the credential of a deleted Target stayed valid and could, with a re-made Target of the same name, receive the new desired state incl. secret values before the approval. Now: 401 on every link route, also after a restart; Recover finishes a cut-off delete; reports without a binding are dropped. QA: 7 mutants, one survived the first tests and has its own test now. Open, low: one Issue from a report in flight during the delete.
- **T-056 (2026-11-11): pending guest config and the restart setting per Target** (PRs #94 plan, #96 backend, #100 web, #103 host log; D-190, D-191, D-193 … D-196; migration 0007). A Target has `allow_restart` (default no). With no, a cores/memory change is applied and waits for a restart, and the Target shows "Restart needed"; with yes the Plan shows the restart. A pending change from outside is drift at once. Real host: H1 … H5 pass, T-015 run 8 passes with the sharper assertion. Cores checked on the host later (PR #109: all pass). Covered by tests only (not provoked on the host): a write that leaves a pending value, a failed restart and the 2-minute hard power-off.
- Installer update defect (2026-11-11, PR #102, found on the real host): a repair run pushed the new binary onto the running one, `pct push` failed with "Text file busy" but exited 0, and the installer restarted the old binary and reported success. Now every pushed file goes to a temp name, is checked by SHA-256 inside the LXC and renamed; the run fails on a wrong copy, on a running binary that is not the new one, and on a crash loop.
- **Host campaign (2026-11-11, PR #98):** `--remove` and a fresh install from main on the test host pass (found and fixed: Proxmox leaves files in `/var/lib/lxc/<id>`, so remove + install with the same id failed); T-055 host checks 1 … 6 pass (D-188, D-189: template re-made from the import file, old guest no drift, 2 Targets with 1 Image); final T-015 run on the fresh install passes (Debian static + Ubuntu DHCP, 264 s). The controller runs on the test host; credentials are in `~/.config/plump/testhost-*` on the dev VM.
- **T-015 (2026-11-11): slice 1 accepted on the real Proxmox host** (PR #95, log `docs/tasks/T-015.log.md`): over the public API with a token; Debian 13 with a static address (about 82 … 109 s) and Ubuntu 26.04 with DHCP (240 s): VM ready, agent drift opens an Issue in 2 s, revert and accept, reboot keeps the SSH host keys, a memory change in Proxmox opens a `guest:config` Issue. Known limits: (1) D-192, a stock Ubuntu 26.04 image takes a static address only from the second boot: use DHCP until T-039; (2) a cores/memory change on a running VM, also by a revert, is only pending until a restart and Plumb does not say so yet: T-056. Found and fixed on the way: stale Image binding (T-055).
- T-054 (2026-11-11): installer minors from the real host (PR #89): a typed-ahead answer is no longer lost, the dry run is quiet about key init when the key exists, Enter prompt after the recovery key.
- **T-053 (2026-11-11): `plumb serve` uses the Proxmox driver; the controller runs on the real host** (PR #87; D-182 … D-184). Start-up check before the data dir, exit 78 for failures a restart cannot repair. Real host: repair run of the installer, service up with 0 restarts, UI + first login + password change, 5 of 5 negative starts, hookscript check, a new VM provisioned and enrolled in 32 s, SSH host keys kept after an outside cloud-init change (D-185), delete through a Plan (token refused, admin approved). Open: `--remove` and a fresh install on the host (after T-015).
- **T-013 + T-052 (2026-11-11): minimal installer, proven on the real host** (PR #26): six roles with exact rights, user, token, pool, two storages, unprivileged LXC with a key volume, `plumb key init` on a TTY; a half-installed host is repaired; 12 of 12 forbidden token actions refused by the real Proxmox. Found by real runs and fixed: token ACL order, umask at `pct create`, template architecture. Decisions D-081 … D-084, D-177 … D-179, D-180 (user: an agent may run the installer on the test host only). Open real-host items moved to T-053.
- **T-012 (2026-11-11): Proxmox VM driver, proven on the real host** (PR #20): 22 host items, `make test-pve` runs the whole VM life cycle inside the LXC (token stays there), 69 recorded real answers as replay test data with a leak-scan test. Found on the host and fixed: stale `template` flag, cleanup that hid errors, parallel image fetch race. Decisions D-076 … D-079, D-185 (cloud-init inputs are create-only, guests keep SSH host keys), D-186 (no package upgrade at first boot, user), D-187 (DNS set at create). Not tested: bridge change on a real guest (the host has one bridge).
- **T-044 (2026-11-11): order between Block uses.** A Block names the resources it waits for (`spec.after`) and the services it restarts (`spec.restarts`); the starter Block `nginx-site` is back. Plan approved by the user (D-176), decisions D-167 … D-175, D-181. PRs: #77 plan, #78 contract (T-046), #80 state + plan (T-047), #83 agent (T-048), #81 + #82 web (T-049), #84 Block (T-050), #85 QA (T-051). Proof: slice test (fails without `after`) and a guest test in CI on Debian 13 and Ubuntu 26.04 (`nginx` + two sites, first Run, both orders). Accepted limit: a site file that Nginx cannot read can stop Nginx at the restart; no agent version gate yet (D-169).
- Team setup, auto mode, GitHub remote, model split.
- T-001, T-002, T-016, T-019: architecture, review, decisions D-001 … D-041, slice cards.
- T-003: Go scaffold, contracts, migration 0001, Makefile, CI (PR #3).
- T-008: starter Blocks deploy-user, motd, nginx (PR #6).
- T-017: OpenAPI contract, web skeleton (PR #2).
- T-004: state package, migration 0002 (PR #7).
- T-009: agent link, event stream, fake driver, race step in CI (PR #11).
- T-010: plan/approve/apply engine, issues, crash recovery, D-075 (PR #14).
- T-011: HTTP API, sessions, token tiers, SSE, embedded UI + agent binary, serve --dev (PR #17).
- T-023: dev login + demo data, dev only (PR #23).
- T-006: web UI slice, 8 screens (PR #19).
- T-024: dev mode simulated agent for new targets (PR #30).
- T-014: UI on the real API, Playwright e2e (PR #28).
- T-025, T-027: slice 2 plan (PR #32) and API contract (PR #33), approved by the user (D-101).
- T-030: token API (PR #34).
- T-034 mock part: history UI (PR #35).
- T-028: secret store (PR #37), D-102 … D-109, D-130.
- T-035 mock part: settings UI (PR #45).
- T-034, T-035 e2e on the real API (PR #51).
- Two flaky tests fixed, both test-side, no product bug (PR #75, #76; D-166).
- T-020: resource type symlink, nginx default site as a link, D-162 … D-165 (PR #73).
- T-043: agent refuses an unsafe state directory at start, D-161 (PR #72).
- T-042: agent loop as root in a real guest; start-up cleanup; apt stderr canary (PR #69, #70).
- Guest harness follow-ups from the run review (PR #66, #68).
- T-015 part without Proxmox: slice 1 acceptance over HTTP with the real agent (PR #67).
- T-021: flow test with the real agent, incl. slice 2 cases (PR #21).
- T-022: agent drift flush survives a permanent 4xx (PR #27).
- Review of the first real guest run (PR #65): harness, resources as root proven; gaps listed.
- T-005 resources (PR #8), T-007 guest harness (PR #5), T-018 agent loop + drift (PR #13): merged by the user; D-054, D-055.
- T-041: all waiting slice 1 PRs in line with main; real agent checked against slice 2; D-159, D-160 (PR #63); agent temp file cleanup (PR #64 into #13).
- T-037: slice 2 acceptance and secret leak hunt (PR #59, #61).
- T-036, T-035 e2e on the real API (PR #60, #62).
- T-033: export/import, encrypted secret bundle, D-150 … D-155, D-158 (PR #56).
- T-029: secret references, D-132 … D-139, D-156 (PR #49).
- T-032: ignore actions, Plan.human_only, migration 0005 (PR #53, local squash), D-145 … D-149.
- T-040: bad-bearer limit scope (PR #55), D-157.
- T-036 mock part: secret parameters, human-only Plans, ignore actions (PR #48).
- T-031: revision API, rollback through a Plan (PR #39), D-115, D-125 … D-128.
- D-131 Plan.rollback_to in the contract (PR #44); closed schemas (PR #46).
- T-026: QA crash recovery test ported to main (PR #31); stale PR #16 closed.

## Dev VM notes
- `jannes` is in the `docker` group since 2026-11-10; local guest runs are possible. Agent sessions that started before that do not have the group yet: run `sg docker -c "make test-guest"` (plain `docker` works after a session restart).

## Open for the user
- `Sys.Audit` (D-239): only the path `/` works. On `/` (no propagation) it also lets the token read disk lists and SMART, the PCI list, the node's hosts file and config, cluster status and config, backup jobs, HA, metric server config, firewall options, realm sync jobs. No writes. Yes or no?
- Known limit (D-192, D-220, D-223): a STOCK Ubuntu 26.04 image used directly can stay on DHCP until its first restart; an Ubuntu version built by Plumb does not have the limit.

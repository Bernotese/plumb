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
| T-038 LXC targets (slice 2 part B, second) | child cards T-075 … T-083, T-085 | plan approved by the user (D-238). H10 done (PR #153): the token cannot make a privileged container, a dangerous feature, a host mount or a device. **On main:** T-075 contract (#154), T-076 `ctpack` (#156, incl. the CA pin of the first-boot download for VMs and LXC), T-077 installer (#157), T-078 agent (#161), T-080 engine (#159), T-081 web (#165), T-079 PR A driver lifecycle (#162), PR B fetch + host recordings (#167) and PR C outside additions (#169): **T-079 done**. T-085 Image status for `lxc` (#171) merged. T-082 LXC image builds (#174, migration 0011) merged. T-082 web part (#176) merged (D-251 no `ssh_keys` for a Target from a built lxc Image, D-252 upgrade by the agent at seal). T-086 test flake fixed (#173). **All code cards are on main. T-083 real-host acceptance: all PASS (PR #178; log `docs/tasks/T-083.log.md`)**: upgrade path with migration 0011 on the live DB, Debian 13 static + Ubuntu 26.04 DHCP containers, drift, live cores/memory, re-enroll, delete, file reuse, link, disk gate, same VMID, stop during a fetch, and two real LXC image builds whose clones enroll. Test host: on main `8af26b0`, clean. Closing work done: fetch-limit tests (#179), T-087 audit findings (#180: Go 1.27.4, `minimatch` override; 2 low `npm audit` findings stay, `deepmerge`, dev-only, not in `web/dist`). **T-038 is done.** Notes, no defects: the `nginx` Block's default site answers 403 on Debian/Ubuntu (index file name); repacked files are per Image name. |
| T-084 token gets `Sys.Audit` (D-239, D-240) | pl-backend | Done. PR #181 and #184 merged. Repair run on the test host ok: role `PlumbAudit` (`Sys.Audit` only) on `/`, propagate 0, user and token; the token reads the thin-pool metadata use (host test `TestHostDiskSpace` PASS); node status and syslog stay 403. Not shown with a real Image build Plan (no Image on the controller). Log: `docs/tasks/T-084.log.md`. |
| T-088 `nginx` and `nginx-site` Blocks: default page answered 403 on Debian/Ubuntu | done | merged (PR #182, D-255): `index index.html index.htm index.nginx-debian.html;`. An existing install keeps its stored Block text: copy that line into the stored `nginx` / `nginx-site` Block. |
| T-089 slice 3 ("Blocks for every day") | all | **All cards T-090 … T-104 are on main; real-host acceptance T-100 passed.** Not re-run on the host: the fixes T-102 … T-104 (proof by CI guest tests; T-104 by the T-100 test Block). The test controller runs 31c6fda, not the last main. The second host run is T-106. |
| T-090 contract (C1) | pl-architect | merged (PR #187, D-266 … D-270) |
| T-091 agent self-update (C2) · T-092 controller update + gate (C3) | pl-backend | both merged (PR #190, #196; fix of two races PR #200; D-271 … D-273, D-275 … D-278; migration 0012) |
| T-093 resources `apt_repository`, `unit`, `sysctl` (C4) | pl-reviewer | merged (PR #191, QA PR #194; D-274: every `plumb-agent*` unit name refused) |
| T-094 resource `command` (C4b) | pl-architect | merged (PR #189; QA PRs #192, #195) |
| T-095 starter Blocks (C5) | pl-frontend | merged (PR #197, D-279 … D-281): `ssh-hardening`, `common-tools`, `shell`, `docker-engine` + `docker-compose` (split, several projects per Target; VM only) |
| T-096 Blocks screen (C6) · T-097 agent version in the web (C7) | pl-frontend | both merged (PR #188, #198; D-263 … D-265, D-282) |
| T-098 QA debt (C8) · T-099 QA per card (C9) | pl-codex-qa | merged (PR #186; #192, #194, #195, #199: update hash check and gate, 8/8 mutants killed) |
| T-100 real-host acceptance (C10) | pl-backend | done, log merged (PR #201). PASS: manual step for old agents (VM + LXC), update by Plan, forced auto-revert, Docker in VM and in an unprivileged LXC (no `keyctl`), build Plan without the metadata note, gate. Two medium findings → T-102, T-103. Host clean. |
| T-101 server checks Block resource attributes at save (found in T-096) | pl-architect | merged (PR #193) |
| T-102 update result lost at the swap (T-100 F1) | pl-backend | merged (PR #202, D-284, D-285) |
| T-103 first apply of a unit + its service needed a 2nd Plan (T-100 F2) | pl-reviewer | merged (PR #204, D-286) |
| T-104 `docker-engine` also for LXC | pl-frontend | merged (PR #203, D-283): one Block for VM and LXC, no `sysctl` |
| T-105 service stopped + enabled with an absent unit: the Plan now shows the enable (follow-up of T-103) | pl-codex-fix | merged (PR #205) |
| T-106 second short host run (2026-11-13): controller on main e909458; T-102 (75 s lost link), T-103, T-104, T-105 passed on the real host; finding F3 → T-108; host clean | pl-backend | done (log PR #207) |
| T-108 a short link drop (30 s) during an agent update no longer fails the Run with a false "old binary" (T-106 F3) | pl-backend | merged (PR #209, D-297, D-298); CI test replays the host timeline |
| T-107 slice 4 ("Safe day 2"): backup + restore of Plumb, Proxmox day 2 (power, tags, disk grow), `firewall` with safe apply, raw mode | all | **Done 2026-11-13.** All cards T-110 … T-137 are on main (63790b7); real-host acceptance T-122 passed (restore proven on the third try, after the fixes T-130 and T-132). No new token right. The test controller ran bf42f61 at the acceptance (71c14c0 since T-139) (before T-133 … T-137; none of them was re-run on the host). Left for later: user management (no way to make a second user), `network` and more NICs, guest adoption |
| T-110 contract backup/restore · T-111 contract day 2 + firewall | pl-architect | both merged (PR #211, D-299 … D-301; PR #214, D-302 … D-304, D-311: a restore sets a new admin password) |
| T-112 backup in the controller · T-113 restore + restored mode · T-116 agent safe apply + `firewall` | pl-backend | T-116 merged (PR #213, D-305 … D-307; CI guest job with unprivileged LXC + VM); T-112 merged (PR #220, D-315 … D-317); T-113 merged (PR #222, D-318 … D-320; three review rounds) |
| T-126 restore follow-ups: one-time password for locked users (human only), renamed guests, durable mark for a foreign guest on a reused VMID (never deleted) | pl-backend | merged (PR #224, D-321, D-322; three review rounds) |
| T-125 raw mode in the API (`Issue.source`, `Issue.raw`, `TargetView.raw`). Also closed: Accept could adopt forged firewall drift without the SSH rule; every Accept on `firewall:*` is refused now | pl-backend | merged (PR #218) |
| T-114 installer (copy timer, `--remove --keep-guests`, `--restore`) · T-115 Proxmox day 2 | pl-reviewer (pl-architect reviews) | T-115 merged (PR #217, D-308 … D-310; main build green after it); T-114 merged (PR #221, D-312 … D-314; no host run yet, T-122 does it) |
| T-117 agent command `grow` | pl-codex-fix | merged (PR #212) |
| T-121 flaky `TestServeNoAnswer` (a freed port was reused by another test; test-only fix) | pl-codex-fix | merged (PR #210) |
| T-118 web slice 4 · T-119 starter Block `firewall` | pl-frontend | T-118 merged (PR #227); T-119 merged (PR #219) |
| T-120 QA slice 4: mutation audits of backup crypto (20/20), restored mode (44 attempts, 0 survivors), safe apply + firewall (40, 2 only killable in CI guest tests, checked by reading) | pl-codex-qa | all three parts merged (PRs #223, #225, #226) |
| T-122 slice 4 real-host acceptance | pl-backend | **passed 2026-11-13** (log PR #235). Third `--restore` ok after the fixes T-130, T-132. Proven: wrong-key and wrong-pool refusals, `--remove --keep-guests`, restore, restored mode, confirm check, `guest:missing`, a foreign guest on a reused VMID stays, token with exactly 7 ACL lines, off-LXC copy + verify, tags, stop/start, LXC and VM grow incl. a retried grow, firewall rollback after a cut link, `drop` without SSH refused. Host clean: only LXC 100 on bf42f61, `plumb-backup` with 3 copies. Findings F1 … F9: fixed F1, F2, F4 … F7; F8, F9 → T-134; F3 (no way to make a second user) for a later slice |
| T-127 Plans that adopt guest data ("Accept", raw mode) are human-only (user decision 2026-11-13, D-323) | pl-reviewer | merged (PR #228) |
| T-128 installer: no stray Perl line for a missing storage; host `/etc/plumb` never wider than 0700 (T-122 F1, F2) | pl-codex-fix | merged (PR #229) |
| T-130 `--restore` failed on every host: the installer put the backup into the data dir (T-122 F5). Now a dir of its own; a test runs the real `plumb` on the installer layout | pl-backend | merged (PR #231); main build green after it (24eb562) |
| T-132 installer: `pct exec` without a tty, bounded wait for the boot of the LXC, checksum check after a failed push (T-122 F7); complete dry-run of a restore rerun (F6) | pl-backend | merged (PR #233) |
| T-134 host-run findings F8, F9; also: a schema error never repeats a rejected value (a pasted private key came back in the 422) | pl-backend | merged (PR #236, D-325) |
| T-135 README: backup and restore, day 2, firewall (after the host proof) | pl-frontend | merged (PR #237) |
| T-136 CI: install tests run in parallel, each script run bounded; "lint, test, build" needs 8 min of 15 (was 11.7–13.6, cancelled on slow runners) | pl-backend | merged (PR #239) |
| T-137 a restored Plumb keeps its off-host copy: the installer keeps the backup storage name over `--remove --keep-guests` | pl-codex-fix | merged (PR #238) |
| T-138 road to the release (all open points of BRIEF §5/§6 in slices 5–9, `docs/plans/ROADMAP.md`) + plan of slice 5 ("People, network, trusted images") | pl-architect, check: pl-reviewer | **slice 5 done 2026-11-14**: all cards T-144 … T-171 on main, real-host acceptance T-158 passed (findings F1–F4 fixed, F5 gets a CI proof in T-184). Plan merged (PR #241), D-327 … D-341. Child cards: T-144 … T-159. User questions U1 … U5 sent 2026-11-13 (needed from slice 6), no answer yet |
| T-139 prep: facts from the test host for the coming topics (read-only) + test controller to latest main | pl-backend | **done**: facts on branch `pl-backend/T-139-prep-host-facts` (no PR); the test controller runs 71c14c0 since 2026-11-13 (repair run, 7 ACL lines, storage.cfg unchanged) |
| T-140 prep: risk draft for user management, guest adoption, SSH repair fallback, MCP / AI help | pl-reviewer | done: branch `pl-reviewer/T-140-risk-draft` (plan file only) |
| T-141 prep: gap list BRIEF §5.2 against the web UI | pl-frontend | done: branch `pl-frontend/T-141-prep-web-gaps` (plan file only) |
| T-142 prep: gap list BRIEF §5.4 … §5.9 against the code | pl-codex-qa | done: branch `pl-codex-qa/T-142-prep-code-gaps` (plan file only) |
| T-143 small leftovers of slice 4 (README sentence, two test nits) | pl-codex-fix | merged (PR #240) |
| T-144 slice 5 C1a: contract (catalogue, users and roles, copy rule, `verify` + refresh) | pl-architect | merged (PR #243), D-342 … D-347 |
| T-149 slice 5 C5: netplan probe in the CI guests | pl-codex-qa | merged (PR #242) |
| T-153 slice 5 C9a: web gaps that need no new API | pl-frontend | merged (PR #245) |
| T-155 slice 5 C10: npm audit refresh | pl-codex-fix | closed without a change (2026-11-13): 2 low findings via `deepmerge` 4.3.1 (dev-only), the advisory has no patched version and the latest `@sveltejs/vite-plugin-svelte` needs it. No downgrade, no suppression. Check again in slice 9 |
| T-160 facts: network setup of the real Debian 13 cloud image (for the `network` contract T-145) | pl-backend | done (branch `pl-backend/T-160-debian-network-facts`): the pinned image has netplan (networkd backend, `50-cloud-init.yaml`, match by MAC); on `pve-dev` an unchanged `netplan apply` kept IPv4 and the agent link (SLAAC IPv6 gap ~2 s). Host clean, pool empty |
| T-145 slice 5 C1b: contract `network`, `group`, generic safe apply | pl-architect | merged (PR #244), D-348 … D-350 |
| T-146 slice 5 C2: starter catalogue API | pl-backend | merged (PR #246) |
| T-147 slice 5 C3: users and roles (migration 0017; closes F3 of T-122) | pl-reviewer (implementer), review: pl-architect | merged (PR #250), D-351 |
| T-148 slice 5 C4: copy rule for guest-reported values (D-331, migration 0018) | pl-backend | merged (PR #248), D-352, D-353. The red race job was real: writer starvation by back-to-back write txs (fixed, measured in CI) |
| T-163 copy rule: write load of hostile guests | pl-codex-fix | merged (PR #256). Open for slice 6: per-agent rate limit for drift reports |
| T-150 slice 5 C6: generic safe apply + resource `network` | pl-backend | merged (PR #249), D-354 … D-357. CI guest test in both cloud VMs: 6 steps incl. forced rollback and timer restore without the agent |
| T-164 `network`: review notes of #249 + boot unit `plumb-agent-network.service` | pl-backend | merged (PR #261), D-358 |
| T-165 CI: race detector as its own job (the job "lint, test, build" hit its 15 min limit again) | pl-codex-qa | merged (PR #263): check ~7 min, race ~8 min, both with a 15 min limit |
| T-152 slice 5 C8: signed checksums | pl-architect (implementer) | merged: part A (PR #247), part B (PR #255, migration 0019), follow-up (PR #262) |
| T-154 slice 5 C9b: web (catalogue, users page, role-aware buttons, Image `verify`) | pl-frontend | merged (PR #252); "Refresh base" tested on the mock only until T-152 part B |
| T-157 slice 5 C12: QA | pl-codex-qa | merged: part 1 (PR #251), part 2 (PR #257), part 3 (PR #260: network rollback 10/10, trust 9/9 reachable mutants killed) |
| T-161 dev demo: `motd` matches the starter catalogue | pl-codex-fix | merged (PR #253) |
| T-162 agent link: store error gives 503 + retry, not 401 (an agent drops its credential on 401) | pl-codex-fix | merged (PR #254) |
| T-151 slice 5 C7: resource `group` (+ inferred order: group before the user that names it) | pl-codex-fix | merged (PR #258) |
| T-156 slice 5 C11: starter Block `network` | pl-frontend | merged (PR #259) |
| T-158 slice 5 C13: acceptance on the real host | pl-backend | **passed** (PR #265, log `docs/tasks/T-158.log.md`): steps 1–8, F2 fixed by T-166 and proven; migrations 0017–0019 on the live DB; agent link survives a `network` apply; reboot in the window restores (D-358); leak hunt 0 hits; host clean (only LXC 100, 7 ACL lines, `storage.cfg` unchanged); controller runs a190dba. Not provable on the host (F5): the `system` approval of a newer signed pin, CI proof only |
| T-167 `network`: main NIC entry by MAC match (host finding F3) | pl-backend | merged (PR #268) |
| T-168 Plan screen shows the document changes of a Plan (host finding F1) | pl-frontend | merged (PR #269) |
| T-170 API: a pending Plan lists its document changes (`Plan.documents`) | pl-architect (implementer) | merged (PR #267) |
| T-159 slice 5 C14: README (users, catalogue, `network`, `group`, signed images, Plan document changes; 4 new pictures) | pl-frontend | merged (PR #270) |
| T-171 users page: role hints from the contract | pl-frontend | merged (PR #271) |
| T-172 plan of slice 6: part A (no user answer), part B (SSH repair, adoption; waits for U1/U2) | pl-architect, check: pl-reviewer | merged (PR #272), D-360 … D-376. Cards T-173 … T-196 |
| Slice 6 part A, merged: T-173 A1 contract (PR #276), T-175 A3 Accept to a Block parameter (PR #281), T-177 A4b nested forms + YAML mode (PR #278), T-178 A5 MCP server `plumb mcp` (PR #280), T-181 A7 live drift of ip/nft (PR #277), T-183 A9 MAC checks (PR #273), T-184 A10 F5 proof in CI (PR #275), T-185 A11 leftovers (PR #274), T-196 A15 form writes only non-defaults (PR #283), T-197 e2e race (PR #279), T-199 flaky netwatch test (PR #284), T-174 A2 settings server (PR #282), T-180 A6b drift report limit (PR #287), T-182 A8 `firewall-off` (PR #285), T-186 QA A3 (PR #286), T-198 installer keep range (PR #292), T-200 firewall test diagnostics (PR #288), T-202 agent resends drift after a 429 (PR #289), T-176 A4a web (PR #291), T-179 A6a token rate limit (PR #296), T-186 QA MCP + live watch (PR #294), T-203 SQLite path escape (PR #297) | – | done |
| Slice 6 part A, waiting: T-188 A14 README (after the host acceptance T-192) | – | waits |
| Slice 6 part B (U1 (a) → D-377, U2 (a) → D-378): merged: T-189 B1 contract (PR #290), T-190 B2 repair core (PR #295, #301; migration 0021), T-191 B3a SSH repair transport (PR #298; migration 0022; D-393), T-193 B4 adoption (PR #299; D-391, D-392), T-195 QA of the repair core (PR #300). In work: T-194 B5 web (pl-frontend), T-195 QA of T-191 and T-193, small follow-up of #299 (L1, L2). Then: T-192 host acceptance of part A + B, T-188 README | – | in work |
| T-169 `group`: the Plan of a create shows the gid (host finding F4) | pl-codex-fix | merged (PR #266) |
| T-166 copy rule: opaque guest reports (`firewall.table`, `network.file`, `apt_repository.sources`, `sysctl.file`) and file/unit lines (host finding F2) | pl-architect (implementer) | merged (PR #264), D-359; step 4 is repeated on the host |
| T-129 token removal leaves its 7 ACL lines on PVE 9.2 (T-122 F4): the installer removes them itself; the token ACL set is unchanged | pl-reviewer | merged (PR #230, D-324) |
| T-123 `README.md` for users (no development docs; the two `make build` lines stay until there is a release, user decision (a); backup part after slice 4) | pl-frontend | merged (PR #215); backup part open |
| T-131 screenshots for the README: 7 pictures of the dev demo (light theme, 52–132 KB), each checked by pl-reviewer and pl-lead; demo data on the documentation range | pl-frontend | merged (PR #232) |
| T-133 example addresses in tests, docs and demo data use the documentation ranges (53 files; history not rewritten). Leak check by pl-lead after the merge: 0 hits for every address, URL, file path and key name value of the test-host config; only the node name and a storage name occur (generic names, known) | pl-frontend | merged (PR #234) |
| T-124 GitHub releases with ready binaries and checksums: prepare only; **a release needs the user's own yes** | pl-codex-qa | merged (PR #216): manual workflow, draft only. No release and no tag exist |
| T-109 flaky `TestAgentUpdateCommand` (test asserted too early; product safe) | pl-codex-qa | merged (PR #208) |

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

## Open technical points (pl-lead watches)
- 2026-11-14 05:36–09:23 UTC: the herdr server was down (killed from outside). Work in flight was local only; no host run was active. pl-lead checked worktrees, PRs and the host after the restart.
- `TestGuestFirewall` failed once in the VM job (PR #281, 2026-11-14): port 8080 not reachable although the table accepts it. Not reproduced, cause unknown, not proven test-only. T-200 adds a dump for the next failure. Read every red VM job before a rerun.

## Open for the user
- Answered 2026-11-14: U1 … U5 (D-377 … D-381). Still to ask when it is time: U6 release steps, U7 repair run of the user's own install for the new ACL lines of slice 7.
- **Working rule D-326 (user, 2026-11-13):** no release until all of BRIEF §5/§6 is done; the team plans and builds slice after slice on its own and sends short notes. The user is asked only for: new token rights or ACLs, public steps, anything outside `pve-dev`, changes to the user's own installation.
- First release: **rests** (D-326). The workflow of T-124 is ready and stays unused.
- Slice 4, user question 1: a real install needs the name of the backup storage from the user (`--backup-storage`). For the test host pl-lead proposes one (T-122).
- Guests made before slice 3 need one manual root command per guest so that their agent can update itself (`docs/ARCHITECTURE.md` §5).
- (Decided 2026-11-12: `nginx` card yes, T-088. pl-lead decided: no card for the double upload of a repacked file by two Images with the same base.)
- Known limit (D-192, D-220, D-223): a STOCK Ubuntu 26.04 image used directly can stay on DHCP until its first restart; an Ubuntu version built by Plumb does not have the limit.

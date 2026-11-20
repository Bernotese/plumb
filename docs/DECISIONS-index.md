# Decisions index

The current reading aid of `docs/DECISIONS.md`. **Before you rely on a decision, look it up here:** a later decision may have changed or replaced it, its text may be out of date, or its state may differ from its line. Made by T-273 (2026-11-20) from the decision texts and the code; it adds no decision. A new decision that changes an older one adds a row here in the same PR.

## Changed or replaced

| Decision | Changed by | What changed |
| --- | --- | --- |
| D-022 | D-091, D-278 | partly; D-278: an agent_update Plan does not go stale when the head moves |
| D-025 | D-291, D-187 | partly; disk is no longer create-only (guest resize exists); D-187 sets nameserver/searchdomain at create |
| D-027 | D-167 | partly; `after` no longer waits for a real Block |
| D-031 | D-046, D-329 | partly; D-329: destructive Plans need the admin role, not any human session |
| D-032 | D-081 | role model replaced |
| D-037 | D-047 | partly (wording): “this replaces the "changed flag" wording of D-037” |
| D-044 | D-091, D-092 | ignore_once / ignore_rule exist |
| D-045 | D-091 | partly; Issue resolution enum extended |
| D-046 | D-329 | partly; admin is a role, no longer every human session |
| D-049 | D-050 | partly: “This replaces "numbers as written" in D-049” |
| D-063 | D-191, D-291, D-391 | partly; D-291 makes `disk` grow-only (was create-only); D-391 skips create-only compare for adopted Targets |
| D-068 | D-323, D-362 | partly; D-323 makes every Accept human-only |
| D-069 | D-075 | partly: “this replaces the last step of D-069” |
| D-070 | D-157 | partly: “Bad-bearer limit (changes D-070)” |
| D-072 | D-183 | partly: “replaces the stop text of D-072” |
| D-075 | D-286 | partly (one exception) |
| D-077 | D-189, D-209 | partly; D-209 closes its open point (retention) |
| D-081 | D-239, D-240, D-324, D-401, D-405 | partly; Sys.Audit on /, 7 ACLs (D-324), PlumbBridge (D-401), guest backup roles (D-405) |
| D-083 | D-177 | partly (key volume mp0) |
| D-084 | D-229, D-244 | partly (plumb-images content import,vztmpl) |
| D-087 | D-130 | partly (narrowed) |
| D-095 | D-153, D-158 | partly; secrets written last, one transaction |
| D-097 | D-224, D-230 | partly; SHA-512 pin instead of index checksum; design-only status ended |
| D-098 | D-197, D-199 | made concrete: “This makes D-098 concrete (`blocks` moves into `build`)” |
| D-099 | D-201, D-235, D-323, D-368 | partly; list of human-only reasons extended (image-recipe, accept-guest-data, firewall-off, repair, guest-restore) |
| D-153 | D-158 | partly: “Import and secrets, in addition to D-095 and D-153” |
| D-165 | D-167, D-172, D-173 | no nginx-site Block and no spec.restarts is void |
| D-169 | D-259, D-268 | partly; "the controller has no version gate" is no longer true |
| D-173 | D-181 | partly (error names service:nginx) |
| D-176 | D-259 | partly; point (3) "no agent version gate for now" is void |
| D-183 | D-229, D-245 | partly (image storage content rule) |
| D-186 | D-204 | partly (narrowed to Targets) |
| D-188 | D-209 | partly: “closes the open point of D-077 and D-188” |
| D-192 | D-220, D-223 | partly; intermittent (D-220), only stock images (D-223) |
| D-193 | D-194, D-196 | made concrete; setting spec.allow_restart |
| D-194 | D-196, D-248, D-291 | partly; its "proposals until the user confirms" is settled; D-291: allow_restart also means "may stop" |
| D-209 | D-430 | partly: “amends D-209. The delete of a **stock** Image ... also removes its base templates” |
| D-216 | D-385 (amended line) | contradiction, partly; D-216 says a reported address is never the target of a connection; no line names D-216 |
| D-219 | D-239, D-240 | partly; metadata read via new right; D-219 itself still "proposed" |
| D-224 | D-345 | partly; "VM Images stay https:// + sha256" is void |
| D-226 | D-251 | partly (narrowed) |
| D-229 | D-244 | partly: “`install.sh` changes the content of `plumb-images` only when it made or adopts” |
| D-234 | D-253 | made concrete: “D-234 made concrete” |
| D-241 | D-345, D-251, D-252 | contradiction, partly; D-241 "vm refuses sha512" and "lxc refuses build (until T-082)" are void |
| D-258 | D-284 | partly: “Refines D-258 for a lost update result” |
| D-259 | D-276 | partly: “Refines D-259 ("the Run checks again at start")” |
| D-261 | D-263 | made concrete (yaml runtime dependency) |
| D-262 | D-280, D-283 | replaced; D-283 then makes docker-engine one Block for VM and LXC |
| D-267 | D-284, D-297 | contradiction, partly; D-267 "the old sha fails it" changed |
| D-280 | D-283 | replaced (no sysctl ip_forward) |
| D-284 | D-297 | partly: “Refines D-284” |
| D-288 | D-302 | made concrete: “D-288 made concrete” |
| D-289 | D-304, D-312 | made concrete (pct exec dd instead of pct pull) |
| D-290 | D-303, D-311, D-313, D-314 | partly; stricter restore rule |
| D-291 | D-299, D-308 | partly; D-308 changes run order |
| D-292 | D-379, D-399 | More NICs and VLAN tags stay later is void (built, slice 7) |
| D-293 | D-300, D-333 | made concrete and generalized |
| D-294 | D-332, D-348 | network stays later is void; resource built (internal/resource/network.go) |
| D-295 | D-300 | partly: “`allow_no_ssh: true`, which replaces the save-time refusal of D-295” |
| D-296 | D-301, D-362 | partly; raw mode only when no parameter maps |
| D-299 | D-308 | partly (run order changed) |
| D-300 | D-305, D-333, D-349 | partly; one safe-apply mechanism for firewall and network |
| D-302 | D-315 | made concrete: “D-302 made concrete” |
| D-303 | D-318, D-351 | partly: “makes the user `admin` an enabled admin again” |
| D-307 | D-333, D-349 | partly: “generalizes D-293, D-300, D-307” |
| D-311 | D-321, D-351 | partly: “refines D-311” |
| D-313 | D-324 | partly (token ACLs deleted before the token) |
| D-316 | (in place, review L3 of #220) | in-place edit of the line, no later D |
| D-320 | D-322, D-325 | partly: “refines D-320” |
| D-322 | D-325 | partly: “refines D-320, D-322” |
| D-323 | D-331 | partly (copy rule closes the ignore-first path) |
| D-326 | D-438 | partly (planner is pl-lead or pl-backend) |
| D-329 | D-342 | made concrete: “D-329 made concrete” |
| D-330 | D-343 | made concrete: “D-330 made concrete” |
| D-331 | D-350, D-352, D-353, D-359 | partly (opaque attrs, list split, fail closed) |
| D-332 | D-348, D-354 | partly: “refines D-332 after T-149 and T-160” |
| D-333 | D-349 | made concrete: “D-333 made concrete” |
| D-335 | D-344, D-345 | made concrete: “D-335 made concrete” |
| D-337 | (in place, review H1, M1 of #241) | in-place edit of the line, no later D |
| D-339 | D-364 | partly; narrowed, and moved from slice 8 to slice 6 |
| D-340 | D-373, D-378, D-388 | made concrete; "U2 can widen it" answered by D-378 (no) |
| D-341 | (in place, T-155) | in-place edit; still stale (see kind=stale) |
| D-348 | D-354 | partly (main NIC by route to the controller) |
| D-349 | D-357, D-358 | made concrete; D-358 extends D-349 |
| D-350 | D-359 | partly: “refines D-331, D-350, D-352” |
| D-352 | D-359 | partly: “refines D-331, D-350, D-352” |
| D-359 | D-368 | partly (firewall removal always human-only) |
| D-362 | D-362 (amended line, D-362/D-376) | wording fix plus fourth accept_mode `guest` |
| D-366 | D-366 (amended line) | contradiction: original says "before the report is decoded" |
| D-372 | D-385 | partly; made concrete |
| D-373 | D-386, D-391, D-395 | made concrete; refined by D-395 |
| D-374 | D-389 (amended line) | contradiction: D-374 says 0021 repair pins, 0022 adoption; code has 0021_plan_origin_repair, 0022_repair_key, 0023_unbind_pending |
| D-376 | D-362 (amended line, D-362/D-376) | wording fix |
| D-377 | D-382 | made concrete: “D-377 made concrete” |
| D-378 | D-386 | made concrete: “D-373, D-378 made concrete” |
| D-379 | D-401, D-414 | made concrete: “D-379 made concrete” |
| D-380 | D-405 | made concrete: “D-380 made concrete” |
| D-381 | D-422 | made concrete: “makes D-381 concrete” |
| D-382 | D-389 (amended line) | contradiction: D-382 says migration 0021 repair_key; code 0022_repair_key.sql |
| D-383 | D-390 | made concrete: “D-383, D-385, D-387 made concrete” |
| D-384 | D-393, D-397, D-389 (amended line) | partly; pins per binding generation; migration 0021 -> 0022 |
| D-385 | D-385 (amended line), D-390 | partly (amended line adds rules) |
| D-386 | D-391, D-395 | partly: “D-386 said "a missing create-only field is not compared"” |
| D-387 | D-390 | made concrete: “D-383, D-385, D-387 made concrete” |
| D-388 | D-396 | partly (unbind_pending) |
| D-389 | D-389 (amended line) | partly (migration numbers) |
| D-393 | D-393 (amended line) | partly (addition) |
| D-394 | D-438 | reviewers now pl-reviewer and pl-backend only |
| D-407 | D-417 | partly: “amends D-407 ... The backup lineage follows the binding generation” |
| D-408 | D-417 | partly: “amends D-407, D-408, D-410” |
| D-410 | D-417, D-420 | partly (safety backup first) |
| D-420 | D-429 | partly (which pre-restore volumes stay) |
| D-423 | D-436, D-437 | partly: “refines D-423 ... the **slot** (1 … 3) is its identity” |
| D-436 | D-437 | partly (container disks cold) |
| D-439 | D-440 | partly: “refines D-439 ... it never ends when the Plan is made” |

## Text that is out of date

The decision stays; only the named part of its text is old.

| Decision | Old text | Today |
| --- | --- | --- |
| D-016 | MCP and AI help come later on the same API | done: internal/mcp (plumb mcp, D-364), explain_issue prompt internal/mcp/prompts.go (D-422) |
| D-041 | The Proxmox test host comes later (2026-11-11 at the earliest) | done: pve-dev in use since T-015 (D-180, acceptances T-066 … T-263) |
| D-079 | The agent can report addresses later (not in this card) | done: D-212 … D-217, migration 0008_agent_addrs.sql, commit 50f0cf2 (#118) |
| D-085 | Part B (design only, cards `blocked: test host`): LXC targets, image builds | done: image builds (migration 0009_image_version), LXC (internal/driver/proxmox/ctfetch.go, D-224 …) |
| D-097 | LXC targets (design only, blocked: test host) | done: T-038 cards merged (D-224 … D-238), internal/driver/proxmox/ctfetch.go |
| D-098 | Image builds (design only, blocked: test host) | done: D-197 … D-210, migration 0009_image_version.sql |
| D-169 | the controller has no version gate | done: capability gate D-259/D-268 (409 agent-outdated) |
| D-184 | A check at propose time comes later | done (for bridges/tags): 422 network-not-allowed at propose, internal/api/nics_test.go:78 |
| D-194 | the name, the default and the re-enrollment rule are proposals until the user confirms them | done: confirmed by D-196 |
| D-241 | `lxc` needs `sha512`, refuses `sha256` and `build` (until T-082) | done: T-082 merged, LXC builds D-251 … D-254 |
| D-260 | `network` and `firewall` wait for slice 4 | done: internal/resource/firewall.go, internal/resource/network.go (D-295, D-332) |
| D-287 | Later: `network` (D-294), more NICs and VLAN tags, guest adoption, guest backups, SSH repair | done: slices 5 … 7 (internal/repair, internal/api/adopt.go, migration 0027_guest_backup, guest.nics) |
| D-317 | empty in a dev install until T-114 writes them | done: install/install.sh:1555 writes PLUMB_PVE_NODE (T-114 merged, D-312 … D-314) |
| D-326 | user management, `network` and more NICs, adoption ..., SSH repair ..., MCP / AI help, guest backups | partly: the rule stays, but every named open point is built (slices 5 … 8, docs/STATUS.md) |
| D-341 | the 2 low `npm audit` findings (`deepmerge` 4.3.1, dev-only; no patched version exists | done: advisory GHSA-gcqx-w48m-w2h4 withdrawn, known-issue lines dropped in 8c5daf8 (#410); deepmerge 4.3.1 still in web/package-lock.json |
| D-360 | **Part B** ... starts only after t[he user answers] | done: U1, U2 answered (D-377, D-378); part B built (internal/repair, internal/api/adopt.go) |
| D-407 | kept in `guest_backup_lineage` (migration 0026) | wrong number: the table is in 0027_guest_backup.sql; 0026 is issue_target_deleted; no later D corrects it |

## Proposed: state today

| Decision | State (pl-lead, 2026-11-20) |
| --- | --- |
| D-219 | accepted (built: #127, #211) |
| D-421 | accepted (built: slice 8, #360 … #384) |
| D-422 | accepted (built: #367) |
| D-423 | accepted (built: #360, #373) |
| D-424 | accepted (a no-build choice that holds: the main address, the gateway and DHCP do not change) |
| D-425 | accepted (a no-build choice that holds: no strictness for other types; D-433) |
| D-426 | accepted (built: #361) |
| D-427 | accepted (built: #374, #380) |
| D-428 | accepted (built: #374, #375) |
| D-429 | accepted (built: #369, #374) |
| D-430 | accepted (built: #364) |
| D-431 | accepted (built: #363, #371) |
| D-432 | open: the `vmbr9` undo on `pve-dev` (release card C8) waits for U12 |
| D-433 | accepted (a drop list that holds: the dropped items are not built) |
| D-434 | open: waits for U9 (a PBS for one test run); until then the README states the PBS limit |
| D-435 | accepted (a no-build choice that holds: no new right; the README documents the way by hand) |
| D-436 | accepted (built: #360, #366) |
| D-437 | accepted (built: #366, #373) |

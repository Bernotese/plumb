# BRIEF code gaps (T-142)

Checked against `origin/main` at `71c14c0` (2026-11-13). Scope: BRIEF §5.3–5.9; D-326 applies.
“Done” means the named code implements the item; it does not certify host acceptance.
Guest “yes” means a real-system guest test exists, not that it ran in this task.
Paths are repo-relative. Registry proof for kinds: `internal/resource/resource.go:115–132`.

## §5.6 resources

| Brief resource | Exists / implementation | Kinds | Real-guest test |
|---|---|---|---|
| file | yes, `internal/resource/file.go` | VM/LXC | yes, `test/guest/resource_test.go:TestFilesAndUser` |
| template | yes via Block string rendering, no separate type; `internal/state/render.go:367` | VM/LXC via file | not checked for rendered template specifically |
| directory | yes, `internal/resource/directory.go` | VM/LXC | yes, `TestFilesAndUser` |
| package | yes, `internal/resource/package.go` | VM/LXC | yes, `test/guest/resource_test.go:TestPackages` |
| apt repository | yes, `internal/resource/aptrepo.go` | VM/LXC | yes, `test/guest/slice3_test.go:TestAptRepositoryInGuest` |
| service | yes, `internal/resource/service.go` | VM/LXC | yes, `test/guest/resource_test.go:TestService` |
| systemd unit | yes (`unit`), `internal/resource/unit.go` | VM/LXC | yes, `test/guest/slice3_test.go:TestUnitInGuest` |
| user | yes, `internal/resource/user.go` | VM/LXC | yes, `TestFilesAndUser` |
| group | no standalone type; user manages membership only (`user.go:29,174`) | — | no group creation test found in guest resource suites |
| SSH key | yes via `user.ssh_keys`, `internal/resource/user.go:179` | VM/LXC | yes, `TestFilesAndUser` checks authorized_keys |
| sysctl | yes, `internal/resource/sysctl.go` | VM only | yes, `test/guest/slice3_test.go:TestSysctlInGuest` |
| network | no agent resource in registry/schema | — | no network resource test in checked guest suites |
| firewall | yes, `internal/resource/firewall.go` | VM/LXC (capability gated) | yes, `internal/agent/guest_firewall_test.go:TestGuestFirewall` |
| timer/cron | yes timer via unit+service; no dedicated cron type | VM/LXC | yes timer, `TestUnitInGuest`; cron scheduling not checked |
| command | yes, `internal/resource/command.go` | VM/LXC | no resource execution test found in checked guest suites |
| Docker Compose | yes Block, `blocks/docker-compose.yaml:1` | VM/LXC via its resource types | yes, `internal/agent/guest_starter_test.go:TestGuestStarterDockerCompose` |

Guest suites inspected: `test/guest/{resource,slice3}_test.go`, `internal/agent/guest*_test.go`.
Kind declarations do not prove that each test passed on both VM and LXC; that was not checked.
Other §5.6 requirements (parallelism, update lifecycle, UI capability hiding): not checked.

## §5.7 starter catalogue

| Starter | Exists / proof |
|---|---|
| users | yes, `blocks/deploy-user.yaml:1` (deployment account; not full user administration) |
| network | no network Block among embedded `blocks/*.yaml` |
| SSH | yes, `blocks/ssh-hardening.yaml:1` |
| MOTD | yes, `blocks/motd.yaml:1` |
| shell | yes, `blocks/shell.yaml:1` |
| common tools | yes, `blocks/common-tools.yaml:1` |
| Nginx | yes, `blocks/nginx.yaml:1`, `blocks/nginx-site.yaml:1` |
| Docker Compose | yes, `blocks/docker-compose.yaml:1`, `blocks/docker-engine.yaml:1` |

## Other requested brief items

| Item | State | Proof / gap |
|---|---|---|
| §5.3 full state export/import | partly (desired-state transfer done) | `internal/api/export_test.go:TestExportImportRoundTrip`; `/export`, `/import` in `api/openapi.yaml:1869,1969`. Full DB/history recovery is a separate backup path; export alone does not include history. |
| §5.3 rollback to revision | done | `internal/api/revisions.go:143`; `internal/api/revisions_test.go:TestRevisionHistoryAndRollback` returns a plan requiring approval. |
| §5.4 retention | done | `internal/plan/image.go:202` computes removals, pins in-use versions; `internal/plan/build_test.go` covers retention/clone concurrency. |
| §5.4 scheduled rebuilds | done | `internal/plan/schedule.go:20` schedule schema; `internal/plan/schedule_test.go:TestScheduleSlots`, `TestSystemCheck`. |
| §5.4 LXC templates | done | `internal/driver/proxmox/ctfetch.go:3` fetch/check/repack; `internal/driver/proxmox/ct_build.go:108,240` converts build CT and clones template. |
| §5.5 create/change/start/stop/delete | done | `internal/driver/proxmox/driver.go:562,1045,1314,1358`; LXC clone in `ct_build.go:240`; power dispatch in `driver.go:1314`. |
| §5.5 desired CPU/RAM/disks/network/tags + risk plans | partly | `driver.go:761` config includes cores/memory/net0, tags at 780; `api/openapi.yaml:872` plans. Single install bridge enforced (`driver.go:48`); full multi-NIC/network coverage absent from agent registry; complete risk classification not checked. |
| §5.8 strictness | done for package present/exact; other resources not checked | `internal/resource/package.go:13,78` implements version strictness; not a universal strictness setting. |
| §5.8 ignore by rule | done | `internal/issue/ignore.go:69` filters rules; `internal/api/ignore_test.go:TestIgnoreRuleFlow`, `TestRuleHumanOnly`. |
| §5.9 event stream | done | `internal/api/events.go:15` SSE with replay; `/events` in `api/openapi.yaml:2223`; `internal/api/build_events_qa_test.go:TestBuildDriftAndVersionEventsQA`. |
| §5.9 permission tiers | done | `internal/api/api.go:180` route access tiers; `internal/api/accept_humanonly_test.go:21` checks token approval restrictions. |
| §5.9 provenance | done for revisions | `internal/store/revision.go:26` stores author, reason, plan ID; coverage of every non-revision mutation not checked. |

No code or tests changed. No host access. No release workflow or tag (D-326).

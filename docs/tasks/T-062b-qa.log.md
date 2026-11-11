# T-062 part B QA audit (PR #133)

Head: `2b7c7a8`. Scope: local slice, link, plan and fake-driver tests. No host or SSH.

## Criteria against tests

| Part B criterion | Result and evidence |
|---|---|
| Lane order, change/manual, Run API | PASS: TestBuildPlanOfAChange, TestBuildPlanManual, TestContractImages. Schedule remains T-063. GAP: no direct image.version event assertion. |
| Pinned recipe and approval | PASS: TestBuildApprovalChecksCurrentRecipe, TestBuildRunStaysPinnedAfterApproval; latter checks revision identity, not applied content. |
| Human-only and one build | PASS: TestBuildPlanOfAChange, TestBuildPlanManual, TestBuildRunning. |
| Build identity, no watch, no secrets | GAP: no direct applied-value assertion; added TestBuildAppliesOnlyRecipeValues. State rejects secret references (TestImageRefusesSecrets). Link watch guard covered by TestLinkSealGuards. Drift dropping has no dedicated build slice assertion. |
| Seal result, revoke, self-stop, no forced conversion | PASS: TestBuildFailures; normal-agent seal items GAP closed by TestSealItemsOnlyFromBuildResult. |
| Failure cleanup and recovery | PASS: TestBuildFailures, TestRecoverBuild, TestBuildRemovesOldBuildGuest. GAP: enroll/apply timeouts, driver failures and late-conversion recovery/manual-removal output lack direct build slice cases. |
| Disk check and shared reservation | PASS: TestBuildDiskGate, TestDiskGateExactLimit, TestDiskGateReservations, TestDiskGateRefusesCreate. |
| Target newest existing ready | PASS: TestBuildThenTargetFromVersion, TestTargetOfBuiltImage. GAP with extant non-ready template closed by TestTargetSkipsNonReadyTemplates. |
| Retention | PASS: TestBuildPlanManual, TestDeleteBuiltImage, fake.TestImageBuildLifecycle. GAP: lowering keep, changed checksum, missing template and guard-refusal output lack direct build slice cases. |
| Timeouts | GAP: defaults present in code; no direct assertion for all three durations. Seal self-stop timeout covered by TestBuildFailures. |

D-208 uses full clones. Removing an old version used by a full-clone Target is allowed. A linked clone must block removal; the fake lifecycle test checks that guard.

## Mutations

Each mutation was applied alone and restored before the next. Logs are in `/tmp/t062b-*.log` on the QA VM. No product change is retained.
| Mutation | Initial result → catching test |
|---|---|
| Send seal to normal link | KILLED: TestLinkSealGuards |
| Accept seal item from normal agent | SURVIVED → TestSealItemsOnlyFromBuildResult |
| Inject secret-value text into build resource | SURVIVED → TestBuildAppliesOnlyRecipeValues |
| Permit watch to build link | KILLED: TestLinkSealGuards |
| Skip failure revoke | KILLED: TestBuildFailures/apply_fails |
| Skip failure RemoveBuild | KILLED: all four TestBuildFailures cases |
| Force stop before conversion | KILLED: TestBuildFailures/no_power_off_(B1) |
| Permit removal under linked clone | KILLED: fake.TestImageBuildLifecycle |
| Remove last ready version (disable keep/current guard) | KILLED: TestBuildThenTargetFromVersion, TestBuildPlanManual |
| Skip build disk reservation | KILLED: TestBuildDiskGate |
| Skip Target clone disk reservation | KILLED: TestDiskGateRefusesCreate |
| Select non-ready version with extant template | SURVIVED → TestTargetSkipsNonReadyTemplates (failed and building) |

All three survivors fail with the added tests and pass with the original product code. The content mutation injects a sentinel value; it does not model a secret-store lookup. No product code changed.

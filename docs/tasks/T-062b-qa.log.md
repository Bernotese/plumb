# T-062 part B QA audit (PR #133)

Initial head: `2b7c7a8`; refreshed against `14c6688` after concurrent PR updates. Scope: local slice, link, plan and fake-driver tests. No host or SSH.

## Criteria against tests

| Part B criterion | Result and evidence |
|---|---|
| Lane order, change/manual, Run API | PASS: TestBuildPlanOfAChange, TestBuildPlanManual, TestContractImages. Schedule remains T-063. GAP: no direct image.version event assertion. |
| Pinned recipe and approval | PASS: TestBuildApprovalChecksCurrentRecipe, TestBuildRunStaysPinnedAfterApproval; latter checks revision identity, not applied content. |
| Human-only and one build | PASS: TestBuildPlanOfAChange, TestBuildPlanManual, TestBuildRunning. |
| Build identity, no watch, no secrets | PASS: TestBuildAppliesOnlyRecipeValues now asserts applied values. State rejects secret references (TestImageRefusesSecrets). Link watch guard covered by TestLinkSealGuards. GAP: drift dropping and absence of a secret-resolver call have no dedicated build slice assertion. |
| Seal result, revoke, self-stop, no forced conversion | PASS: TestBuildFailures; normal-agent seal items initially GAP; upstream TestSealResultItems and TestSealLinkRulesCombined now cover it. |
| Failure cleanup and recovery | PASS: TestBuildFailures, TestRecoverBuild, TestBuildRemovesOldBuildGuest. GAP: enroll/apply timeouts and some driver failures lack direct build slice cases; upstream TestLeftTemplateShown covers late-conversion output. |
| Disk check and shared reservation | PASS: TestBuildDiskGate, TestDiskGateExactLimit, TestDiskGateReservations, TestDiskGateRefusesCreate. |
| Target newest existing ready | PASS: TestBuildThenTargetFromVersion, TestTargetOfBuiltImage. GAP with extant non-ready template closed by TestTargetSkipsNonReadyTemplates. |
| Retention | PASS: TestBuildPlanManual, TestDeleteBuiltImage, fake.TestImageBuildLifecycle. GAP: lowering keep, changed checksum, missing template and guard-refusal output lack direct build slice cases. |
| Timeouts | GAP: defaults present in code; no direct assertion for all three durations. Seal self-stop timeout covered by TestBuildFailures. |

Upstream TestRetentionSkipsVersionInClone protects an in-progress clone; TestCreateSkipsVersionInRemoval protects the inverse race. D-208 uses full clones. Removing an old version used by a full-clone Target is allowed. A linked clone must block removal; the fake lifecycle test checks that guard.

## Mutations

Each mutation was applied alone and restored before the next. Initial logs are in `/tmp/t062b-*.log`; refreshed logs are `/tmp/t062b-current-*.log` on the QA VM. No product change is retained.

| Mutation | Initial result → catching test |
|---|---|
| Send seal to normal link | KILLED: TestLinkSealGuards |
| Accept seal item from normal agent | SURVIVED initially → upstream TestSealResultItems, TestSealLinkRulesCombined |
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

The two remaining survivors fail with the added tests and pass with unchanged product code. The initial seal-item gap is covered by the updated PR; its duplicate QA test was removed. The content mutation injects a sentinel value; it does not model a secret-store lookup. No product code changed.

All listed mutations were rechecked separately on `14c6688` with focused tests. Seal-result acceptance required bypassing both the agent-kind check and the command-kind check. The extra in-progress-clone retention mutation (skip startRemove) fails TestRetentionSkipsVersionInClone.

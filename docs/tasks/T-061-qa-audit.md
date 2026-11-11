# T-061 narrow QA audit of PR #125

Audited PR head `69b7a7f`. Fake PVE only. No host or SSH.

## Card criteria

- PASS: CreateBuild full clone, name, marker, onboot, user without key,
  upgrade on/off: TestCreateBuild.
- PASS: MakeImage marker/stopped/template guards, one config write, scrub,
  digest, conversion: TestMakeImage, TestMakeImageGuard,
  TestMakeImageStaleDigest. Conversion failure restores build identity for
  engine cleanup: TestMakeImageConversionFails (does not delete the guest).
- PASS: FindImage name/digest, pool/range: TestFindImage. Added explicit
  missing/short/prefixed/suffixed build-line cases in
  TestFindImageRequiresFullBuildLineQA; these were an assertion gap.
- PASS: ListImages versions/base/import files including unreferenced imports:
  TestListImages. Version marker, Observe, Diff and full Target clone:
  TestImageVersionMarker.
- PASS: RemoveImage pool-first/gone, range, pool, template, lock, exact name,
  ownership/digest, purge flags, no imports: TestRemoveImage and
  TestRemoveImageGuard. Driver.Delete: TestDeleteNeverRemovesAVersionTemplate.
- PASS: linked clone disk-line and storage-parent signs, and unreadable
  volume list: TestRemoveImageLinkedClone, TestRemoveImageNeedsVolumeList.
  Neither sign visible is an explicitly tested detection limit.
- PASS: DiskSpace data use, metadata available/403/ambiguous, inactive size:
  TestDiskSpace. Added TestDiskSpaceUnreadableQA for an API failure.
- PASS: fake versions, linked clones, storage accounting:
  fake.TestImageBuildLifecycle and TestImageOpsInjectFailures.
- GAP: image-build replay recording absent; TestReplayImageBuild skips.
  Existing replay/leak scan tests do not supply new image-build recordings.
  Host criteria remain outside this audit.

## Mutations (one at a time, restored after each)

| Mutation | Catching test |
| --- | --- |
| FindImage ignores full ownership/build proof | TestFindImage (other digest); added TestFindImageRequiresFullBuildLineQA also kills it |
| RemoveImage ignores linked clones | TestRemoveImageLinkedClone (disk_line, volume_parent, both) |
| Continue with unreadable volume list | TestRemoveImageNeedsVolumeList |
| Remove QuoteMeta from shared name pattern | TestListImages |
| RemoveImage uses unquoted regexp instead of exact config-name equality | TestRemoveImageGuard (9010) |
| Skip restoreBuild after failed MakeImage conversion | TestMakeImageConversionFails |
| Return free capacity with unknown size | TestDiskSpace (no size) |
| Return free capacity after unreadable data-use API | Added TestDiskSpaceUnreadableQA |
| Drop stopped-guest guard | TestMakeImageGuard (expects guard refusal before conversion) |

Literal QuoteMeta removal alone does not weaken RemoveImage: its separate
exact object-name and config-name equality checks still reject look-alikes.
The shared helper mutation is killed by ListImages. The behavioral deletion
mutation above confirms the RemoveImage regex-character test is effective.

All mutations were killed. Product code is unchanged.

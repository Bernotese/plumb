# T-074 – No guest create when the disk storage would be too full

- **Owner:** tbd (pl-lead)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-061 (`Driver.DiskSpace`), T-062 (the lock and the reservation of the build lanes)   · **State:** draft from the T-039 plan review (S5, 2026-11-11); pl-lead decides when it starts

## Goal
A Plan that creates a guest does not fill the disk storage. Plumb refuses the create with a clear text before it clones.

## Situation
- Host fact B4 (`docs/tasks/T-039.log.md`): the test host has a 42 GB thin pool with no automatic growth. A full thin pool gives I/O errors in every guest on it.
- T-039 adds the rule for image builds: no build when `used + reserved + build disk` is more than 85 % of the disk storage (D-203). A guest create is a full clone of up to the whole `disk` size, so it is the larger risk (review of PR #110, S5).

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` section 5 "Disk space", `docs/decisions/T-039.md` D-203
- `internal/plan/run.go` (`guestStep`, create), `internal/driver` (`DiskSpace`, from T-061)

## Acceptance criteria (draft)
- [ ] Before a clone the engine uses the same check, the same lock and the same reservation as the build lanes: the create fails when `used + reserved + guest.disk` is more than the limit. Nothing is made; the step text names the storage, the use and the limit.
- [ ] Several creates in one Run and a build at the same time count together.
- [ ] The Plan of a create shows a warning when the check would fail now.
- [ ] The limit is one setting for builds and creates (default 85 %).
- [ ] Tests: `make test` (fake driver with a set storage size; two creates at once).

## Out of scope
- Growth of a guest disk after the create. A storage that is not a thin pool needs no other rule: the check uses the real use that Proxmox reports.

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

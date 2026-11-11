# T-074 – No guest create when the disk storage would be too full

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
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

## Plan (pl-frontend, 2026-11-11; smallest rule for D-218 answer 7)
- **One gate in the engine** (`internal/plan/disk.go`): a mutex, the bytes reserved now, the limit. `Reserve(bytes)` reads `Driver.DiskSpace`, refuses when `(used + reserved + bytes) × 100 > limit × total` (exact integers, `driver.OverLimit`, same rule as the T-062 card and the web), else adds `bytes` to `reserved` and returns a release. Check and reservation happen under the one mutex, so parallel creates of one Run (targets run in parallel) and the build lanes of T-062 count together. T-062 uses the same gate for its builds and shows `Reserved()` as `DiskUse.reserved_bytes`.
- **Metadata**: when `DiskSpace.MetaKnown`, a create is also refused while `metaUsed × 100 > limit × metaTotal` (no estimate of the growth per clone).
- **Overflow (review S1)**: sizes are summed exactly (`big.Int`); a disk whose bytes do not fit in an int64 counts as the largest size and is refused. No schema maximum for `disk`: it would make stored documents with a larger disk invalid (a contract break).
- **Read timeout (review N1)**: the `DiskSpace` read under the gate mutex has a 30 s limit.
- **Known limit (review N2)**: the reservation covers the moment of the create. A thin clone can grow later; that growth is not reserved (D-219).
- **Create**: `guestStep` reserves `disk` GiB after `FetchImage` and before the enrollment token and the clone; the reservation ends when `Create` returns (then the real use counts). Refused: the step fails, nothing is made, no token is made; text: `<storage> would be <N> % full with the <disk> GiB disk of <target> (used …, held by running creates and builds …, limit <L> %); nothing was made. Free space on the storage or lower the disk.` `DiskSpace` cannot be read: the create fails with that reason (fail closed, like a build).
- **Plan warning**: `Propose` sums the disks of all creates of the Plan and adds a note (→ `Plan.warnings`) when that sum fails the check now, or when the use cannot be read. No API change.
- **Setting**: `plan.Config.DiskLimitPercent`, default 85; `PLUMB_DISK_LIMIT_PERCENT` (50 … 95) in `plumb serve`. One value for builds and creates.
- **Tests**: fake driver with `SetDiskSpace`: a refused create makes no guest and no token; 85.0 % passes, one byte more is refused; two creates in one Run that fit alone but not together: one fails; a reservation ends after the create; the Plan warning; the setting parse.


## Result (owner, max 8 lines)
Branch `pl-frontend/T-074-disk-check` (stacked on T-061, PR #125) · tests: `make lint test build` → OK.
- Gate API for T-062: `plan.DiskGate` via `Engine.Disk()`: `Reserve(ctx, bytes, what) (release func(), err error)`, `Check(ctx, bytes, what) error`, `Reserved() int64`, `Limit() int`; refusal wraps `plan.ErrDiskFull`, its text names storage, numbers and limit; `plan.GiB`.
- Rule: `driver.OverLimit(need, total, limit)` (big.Int, total ≤ 0 = over); known thin-pool metadata over the limit refuses, unknown does not; unreadable use refuses (not ErrDiskFull).
- Create: `guestStep` reserves after FetchImage, before the token; releases when Create returns. Plan: note via `Engine.diskNote` (sum of the create disks).
- Setting: `plan.Config.DiskLimitPercent`, `PLUMB_DISK_LIMIT_PERCENT` 50 … 95, default 85 (D-219 in `docs/decisions/T-074.md`).

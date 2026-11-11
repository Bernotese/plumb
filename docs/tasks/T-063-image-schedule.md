# T-063 – Image builds: schedule

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-065)
- **Size:** M   · **Depends on:** T-062   · **Parent:** T-039

## Goal
A built Image is rebuilt on its schedule with no click.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §3 (`system`), §6; `docs/decisions/T-039.md` D-201, D-210
- `internal/plan` (`PollGuests` pattern, approval), `image_schedule` (T-059)

## Acceptance criteria
- [x] `plan.Config.Now`; `Engine.RunSchedules(ctx)` ticks every minute.
- [x] A slot is due when it is past and newer than `last_slot`; after a stop only the newest missed slot runs; a new or changed schedule starts with the next slot; a slot is skipped while a build of the Image runs; builds run one after the other; a failed build is not tried again before the next slot.
- [x] `system` approves only inside the engine (no HTTP path), only origin `build`, trigger `schedule`, no document change, no human-only reason, a due slot. A scheduled Plan that went stale (S2) is not approved.
- [x] `ImageView.status.next_build_at` filled.
- [x] Tests: due slots with a set clock (day, week, month, day 28, after a long stop), `system` refuses every other Plan; `make test` passes.

## Out of scope
- Cron text. A notice outside the UI for a failed build.

## Result (owner, max 8 lines)
`pl-architect/T-063-schedule` (stacked on T-062 part B) · tests: `make lint`, `make test`, `make build`.
- `internal/plan/schedule.go`: `Schedule.LastSlot/NextSlot` (UTC), `CheckSchedules` (one round), `RunSchedules` (serve, every minute), `NextBuildAt` (→ `ImageStatus.next_build_at`).
- Due: newest past slot newer than `last_slot`; it is used up before the Plan is made (no retry of a failed or stale build before the next slot; a slot during a running build of the Image is skipped). One scheduled build at a time: a due slot waits for the next round.
- New or changed schedule: every commit sets `last_slot` = now for an Image whose schedule is new or changed, so it starts with the next slot.
- `system`: `ProposeBuild(trigger schedule, author system)` + `Approve(…, "system", SystemCheck)`; `SystemCheck` = origin build, only schedule builds, no document change, no Target, no human-only reason. The API has no principal `system`. A recipe change since the Plan makes it stale (S2), not approved.
- A scheduled build's Run records the disk notes of its Plan (thin-pool metadata unknown) in `image:base`.

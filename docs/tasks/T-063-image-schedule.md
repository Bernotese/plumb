# T-063 – Image builds: schedule

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-065)
- **Size:** M   · **Depends on:** T-062   · **Parent:** T-039

## Goal
A built Image is rebuilt on its schedule with no click.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §3 (`system`), §6; `docs/decisions/T-039.md` D-201, D-210
- `internal/plan` (`PollGuests` pattern, approval), `image_schedule` (T-059)

## Acceptance criteria
- [ ] `plan.Config.Now`; `Engine.RunSchedules(ctx)` ticks every minute.
- [ ] A slot is due when it is past and newer than `last_slot`; after a stop only the newest missed slot runs; a new or changed schedule starts with the next slot; a slot is skipped while a build of the Image runs; builds run one after the other; a failed build is not tried again before the next slot.
- [ ] `system` approves only inside the engine (no HTTP path), only origin `build`, trigger `schedule`, no document change, no human-only reason, a due slot. A scheduled Plan that went stale (S2) is not approved.
- [ ] `ImageView.status.next_build_at` filled.
- [ ] Tests: due slots with a set clock (day, week, month, day 28, after a long stop), `system` refuses every other Plan; `make test` passes.

## Out of scope
- Cron text. A notice outside the UI for a failed build.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-058 – Image builds: contract (schema, OpenAPI, wire seal, driver interface)

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-065)
- **Size:** M   · **Depends on:** –   · **Parent:** T-039 (approved, D-218)

## Goal
The shapes of image builds are fixed in code, so T-059, T-060, T-061 and the mock part of T-064 can start in parallel.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §1 (contract), §4 "Wire", §5 "New methods", §7; `docs/decisions/T-039.md` D-197 … D-208
- `internal/state/schema/image.json`, `api/openapi.yaml`, `internal/wire`, `internal/driver` (+ `fake`, `proxmox`), `web/src/lib/mock/mock.ts`

## Acceptance criteria
- [ ] `image.json`: optional `build` (`blocks`, `upgrade` default true, `guest` {cores, memory, disk, bridge, ip}), `retention.keep` (1 … 20, default 3), `schedule` {every day|week|month, at "HH:MM", weekday, day 1 … 28}. An Image without `build` validates as today. Schema examples for a stock and a built Image.
- [ ] OpenAPI (new parts `x-plumb-planned`): `ImageView.status` {versions[], building, last_build, next_build_at}; `POST /images/{name}/build` → Plan; `Plan.origin` `build`; `Plan.images[]` {image, build {version, trigger, upgrade, resources[]}, removes[]}; `Plan.human_only` `image-recipe`; `Run.images[]`; `TargetStatus.image_version`; event subject `image:<name>`. Edits stay local to the Image parts (T-068 edits Hello/agent parts at the same time).
- [ ] `wire`: `CommandSeal = "seal"`, body `{plan_id, upgraded}` (no path, command or user name); result with one line per step; answer `refused`; the enrollment answer gets a `build` mark.
- [ ] `driver`: `CreateBuild`, `MakeImage`, `FindImage`, `ListImages`, `RemoveImage`, `DiskSpace`, types `BuildSpec`, `BuiltImage`, `ImageRef`, `GuestSpec.ImageVersion`. `fake` and `proxmox` compile with stubs that return a clear "not implemented (T-061)" error.
- [ ] Mock: an Image with three versions, a running build, a stock Image, a build Plan with `removes`, a build Run with its steps.
- [ ] Tests: `make test` and `make lint` pass; OpenAPI and schema checks pass.

## Out of scope
- Behaviour: save checks (T-059), `seal` (T-060), driver (T-061), engine and API handlers (T-062). Migration 0009 is T-059.

## Result (owner, max 8 lines)
branch · commit · tests · notes

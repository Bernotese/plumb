# T-062 – Image builds: engine build lane, retention, API

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-065)
- **Size:** L   · **Depends on:** T-059, T-061 (T-060 for the end-to-end slice)   · **Parent:** T-039

## Goal
An approved Plan builds a new image version, Targets are made from it, and old versions go away.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §2, §3, §5 "Retention", "Disk space", §8 row "Slice"; `docs/decisions/T-039.md` D-199 … D-203, D-207, D-209
- `internal/plan` (`run.go`, approval, `Recover`), `internal/api`, `internal/link`, T-057 revoke, T-073 guard

## Acceptance criteria
- [ ] Triggers `change`, `manual` (`POST /images/{name}/build`), `schedule` (used by T-063); `Plan.images[]`; steps `image:base` → `guest:config` → enroll wait → resources → `image:seal` → `image:template` → `image:retention`.
- [ ] Review S2: origin `build` commits no revision; the Plan records base revision and recipe hash; at approval a different recipe hash at the head = stale; a head change without a recipe change is not stale; the Run renders only from the pinned revision.
- [ ] `image-recipe` human-only reason by content for every origin; `manual` build approved by a human or an `apply` token; a second build of the same Image refused ("build running").
- [ ] Build guest: token marked `build`, link name `build:<name>`, no `watch`, drift dropped; apply with no risk limit.
- [ ] Review B1: after the `seal` result the controller revokes the agent (T-057), sends no stop, waits for `stopped` inside the seal timeout, then `MakeImage`; timeout or forced stop = failed build, guest deleted, never converted.
- [ ] Review S4: every failure and timeout revokes, deletes the build guest with the full T-073 guard, sets the version `failed`; `Recover` and each new build remove guests with the `plumb-build:` marker; the build lane has no secret resolver. A **template** with the build name and marker (a conversion that ended on the host after the driver gave up; T-061 review N1) is no guest for `Delete` (it refuses templates): call `MakeImage` again with that version and digest (it finishes the template), or report it with "remove by hand".
- [ ] Review S5: the 85 % check and the disk reservation under one lock for all lanes; the Plan shows the storage use. Exact rule, no rounding (QA of PR #123): refuse when `(used + reserved + build disk) × 100 > limit × total`, in integer bytes; 85.0 % passes, 85.01 % is refused; a total of 0 is refused. The web uses the same comparison (`overLimit`, `web/src/lib/images.ts`).
- [ ] Target create takes the newest `ready` version that exists; refused at propose time with "build the Image first", and when the same Plan builds that Image.
- [ ] Retention (n2): only after the new version is `ready`; never the version of this Run; never fewer than `keep` ready; also in a Plan that lowers `keep`, changes the base checksum or deletes the Image. Timeouts: enroll 20 min, apply 15 min, seal 5 min.
- [ ] API handlers and events (`image:<name>`) for the T-058 contract.
- [ ] Tests: unit and slice rows of Plan §8 (fake driver, `linktest`, simulated `seal`); `make test` passes.

## Out of scope
- The clock and `system` approval (T-063). A Run that builds and then creates Targets.

## Result (owner, max 8 lines)
Two PRs (pl-lead agreed). **Part A** `pl-architect/T-062-engine` (Plan side, builds off by `plan.Config.ImageBuilds`):
- Migration `0010_plan_images` (`plan.images`); `store.ApproveBuildPlan` (no revision, Run pinned to the checked head), `PendingPlans`; `MarkStalePlans` skips origin `build`.
- Engine: `Plan.images` (build on a new or changed recipe, trigger change; removals preview: ready versions beyond keep, base templates of another checksum, all on delete); `image-recipe` by content; propose refusals (no ready version; same Plan builds it); `ProposeBuild` (manual/schedule); approval of a build Plan by recipe hash (head change without recipe change keeps it; recipe change marks it stale after every commit); one build per Image (`build-running`, also a `building` row).
- API: `POST /images/{name}/build`, `ImageView.status` (versions, building, last build, unused imports with the strict `pvesm free` form, disk), `Plan.images`, `Run.images`, `TargetStatus.image_version`; contract test `TestContractImages`.
- Unused import files (QA of #128): safe to remove when no checksum needs it (document, running build); a ready version does not need it, because a version template is a full copy (D-209 text).
- An image lane fails at `image:base` in part A. Open for part B: the lane, build token + link guard, seal handshake (T-060), revoke + T-073 guard, disk gate of T-074, retention, Recover, Target from the newest version, version Targets list, `image.version` events, flag on.

## Note from the T-064 review (pl-lead, 2026-11-11)
- The list of unused import files feeds a command line that the user runs as root (`pvesm free …`, with a Copy button in the web). The API must list only volume names that Plumb itself made, checked by a strict pattern (`plumb-<name>-<sha256[:12]>.qcow2`, D-077), and give the command in exactly this form, with no quotes: `pvesm free <storage>:import/plumb-<name>-<12 hex>.qcow2` (the strict name holds no shell character). The web offers Copy only for a line that matches `^pvesm free [A-Za-z][A-Za-z0-9._-]*:import/plumb-[a-z0-9][a-z0-9.-]*-[0-9a-f]{12}\.qcow2$` (PR #123, `safeFreeCommand`), so any other form loses the Copy button. A name that does not match is listed without a command. Test with a file name that holds shell characters.

## Note from the T-061 review (pl-lead, 2026-11-11)
- `DiskSpace` fails closed for the data space, but when the thin-pool metadata cannot be read (403 or an ambiguous pool) it gives `MetaKnown=false`. Decision of pl-lead: no new token right now (D-081). The build Plan then shows a clear line "thin-pool metadata use is unknown" so the person who approves sees it; a scheduled build (planned by `system`) records the same line in its Run. After the host check (is the metadata readable with today's rights?) pl-lead decides if more is needed.
- N1: a conversion that times out in the driver but ends on the host can leave a template with the build name: the retention step and the unused list must show it (it is never adopted).

# T-059 – Image builds: save checks, recipe, migration 0009

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-065)
- **Size:** M   · **Depends on:** T-058   · **Parent:** T-039

## Goal
Plumb refuses an Image document that would put a secret into a template, and it stores image versions.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §1 "Checks", "Recipe", "Image version"; §4 guard (c); `docs/decisions/T-039.md` D-197, D-198, D-202, D-207
- `internal/state` (`Validate`, render), `internal/store/migrations` (0007 is the newest on main; 0008 is T-067)

## Acceptance criteria
- [x] `state.Validate`: Blocks of an Image exist and render; a `{secret: …}` value or a `writeOnly` parameter in them is an error; `retention`/`schedule` without `build` is an error; `Target.guest.disk` < `build.guest.disk` of its Image is an error. The check runs on the whole set, so a later change of a shared Block is caught (n3). Review of T-058 (N2): the check also runs on the rendered resources of the Image (`RenderImage`), so a secret marker that a Block template or a default puts in is refused too.
- [x] Review S1 (c), save side: `file`, `directory`, `symlink` with a path in `/etc/plumb-agent`, `/var/lib/plumb-agent`, or on the agent binary, unit or install script are refused for every document, with a clear text.
- [x] `state.RenderImage` gives the flat resource list; recipe = `{type, url, sha256, upgrade, guest.disk, resources}` and its SHA-256.
- [x] Migration `0009`: `image_version` (columns of Plan §1, key `(image, version)`, numbers never used again, also after an Image delete), `image_schedule(image, last_slot)`, `plan.origin` value `build`. Store functions to add, update and list versions.
- [x] Tests (unit): secret refused (direct and through a shared Block changed later), disk rule, recipe hash stable and changes with each part, agent path refused, version numbers after delete. `make test` passes.

## Out of scope
- Engine use of the rows (T-062). The agent side of the path rule (T-060).

## Result (owner, max 8 lines)
`pl-architect/T-059-image-state` · tests: `make lint`, `make test`, `make build` ok.
- `state`: `render` serves a Target and an Image build (`unit`); `checkUses` refuses a Block with any secret parameter in an Image (also without a value, n3), a reference anywhere, and a marker in the rendered resources (N2). Disk rule on the Target. `RenderImage`, `Recipe.Hash`, `BuiltImages`, `ErrNotBuilt`.
- Agent paths: `wire.IsAgentPath` / `AgentPathOf` (shared with T-060): agent dirs, binary, install script, unit, drop-ins and enable links in all systemd unit dirs; `file`/`directory` path, `symlink` path and target (relative too), `user` home. A stored revision from before the rule still loads.
- Store: migration `image_version` (one `building` per Image by index, no delete, fixed columns, status triggers), `image_schedule`, `plan.origin` `build`; `AddImageVersion` (number in the same transaction, `ErrBuildRunning`), `FinishImageVersion`, `RemoveImageVersion`, `ImageVersions`, `BuildingImageVersions`, schedule slot.
- The T-058 guard moved from `state.Validate` to the propose path (`plan.apply`, 422 "not available yet (T-062)"): T-062 removes it.

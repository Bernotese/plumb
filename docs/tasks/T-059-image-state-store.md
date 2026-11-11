# T-059 – Image builds: save checks, recipe, migration 0009

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-065)
- **Size:** M   · **Depends on:** T-058   · **Parent:** T-039

## Goal
Plumb refuses an Image document that would put a secret into a template, and it stores image versions.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §1 "Checks", "Recipe", "Image version"; §4 guard (c); `docs/decisions/T-039.md` D-197, D-198, D-202, D-207
- `internal/state` (`Validate`, render), `internal/store/migrations` (0007 is the newest on main; 0008 is T-067)

## Acceptance criteria
- [ ] `state.Validate`: Blocks of an Image exist and render; a `{secret: …}` value or a `writeOnly` parameter in them is an error; `retention`/`schedule` without `build` is an error; `Target.guest.disk` < `build.guest.disk` of its Image is an error. The check runs on the whole set, so a later change of a shared Block is caught (n3).
- [ ] Review S1 (c), save side: `file`, `directory`, `symlink` with a path in `/etc/plumb-agent`, `/var/lib/plumb-agent`, or on the agent binary, unit or install script are refused for every document, with a clear text.
- [ ] `state.RenderImage` gives the flat resource list; recipe = `{type, url, sha256, upgrade, guest.disk, resources}` and its SHA-256.
- [ ] Migration `0009`: `image_version` (columns of Plan §1, key `(image, version)`, numbers never used again, also after an Image delete), `image_schedule(image, last_slot)`, `plan.origin` value `build`. Store functions to add, update and list versions.
- [ ] Tests (unit): secret refused (direct and through a shared Block changed later), disk rule, recipe hash stable and changes with each part, agent path refused, version numbers after delete. `make test` passes.

## Out of scope
- Engine use of the rows (T-062). The agent side of the path rule (T-060).

## Result (owner, max 8 lines)
branch · commit · tests · notes

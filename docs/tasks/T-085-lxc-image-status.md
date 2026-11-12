# T-085 – Image status for `lxc` Images: unused template files

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-079 PR C (driver lists `vztmpl` files of Plumb)
- Child card of T-038 (follow-up of T-075 and T-079 PR B).

## Goal
Every `lxc` Image has a status that lists its unused repacked template files with the `pvesm free` line (D-233). Plumb never deletes such a file.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §4 (retention), §8; D-209, D-232, D-233
- `api/openapi.yaml` (ImageStatus: the "planned" text of T-075), the Image status code in `internal/api` and `internal/plan`, the driver method of T-079 PR C

## Acceptance criteria
- [ ] `ImageStatus` is present for every `lxc` Image; `unused_imports` lists the unused `vztmpl` files with the strict `pvesm free` line; a file in use by a container or by the current Image is not listed.
- [ ] Retention and Image delete never call `RemoveImage` for a `vztmpl` file (test).
- [ ] The "planned" wording in the contract is removed.
- [ ] Tests: `make lint test build` passes.

## Out of scope
- LXC image builds (T-082).

## Result (owner, max 8 lines)
branch · commit · tests · notes

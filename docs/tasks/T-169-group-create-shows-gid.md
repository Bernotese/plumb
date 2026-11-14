# T-169 – group: the Plan of a create shows the gid (host finding F4)

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex, ≤ ~40 lines)   · **Depends on:** –

## Goal
The Plan that creates a group lists only `state: present`; the wanted `gid` (and `system`) is not among the changes, while a user create lists all its attributes. A person must see the gid before the approval.

## Context (read only these)
- `docs/tasks/T-158.log.md` (PR #265; finding F4)
- `internal/resource/group*.go` (Diff / Observe for an absent group) · how `user` lists its attributes on create

## Acceptance criteria
- [x] A create lists every set attribute (`gid`, `system`), like `user`. No change of what is applied.
- [x] Unit test + the guest test asserts the changes of a create.
- [x] `make lint test` passes; own worktree and branch; PR to `main`; report to `pl-lead`.

## Out of scope
- Anything else of the log.

## Result (owner, max 8 lines)
branch · commit · tests · notes

pl-codex-fix/T-169-group-create-shows-gid · see PR commit · `make lint test` passed.
Guest `TestGroup` passed in disposable Debian 13 and Ubuntu 26.04 containers.
Create lists set gid/system (including false); Apply stays unchanged.
Context log T-158.log.md is absent from origin/main; used the card's F4 description.

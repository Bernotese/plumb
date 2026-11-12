# T-101 – Server checks Block resource attributes at save

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-090 (merged)   · **Parent:** T-089 (found by pl-frontend in T-096)

## Goal
Today the server checks the attributes of a Block's resources only when a Target renders the Block. A Block that no Target uses saves with a bad attribute. With the Blocks screen (T-096) a user hits this. The server must refuse it at save.

## Context (read only these)
- PR #188 (T-096) – the finding and the shared test cases (Go + vitest agree, D-264)
- `internal/resource/schema/*.json`, the Block save path, `schema/block.json`
- `docs/ARCHITECTURE.md` §14

## Acceptance criteria
- [ ] Saving (proposing) a Block with an unknown resource type or a bad attribute is refused with a field error that names the resource, also when no Target uses the Block
- [ ] Attributes that hold template expressions are handled by a clear rule (check what can be checked without rendering; say the rule in the PR)
- [ ] The error has the shape that T-096 maps to a Block line (D-265)
- [ ] Existing stored Blocks still load; a test shows it
- [ ] `make lint test build` passes

## Out of scope
- Web changes (pl-frontend takes a follow-up if the error shape needs it)

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-161 – Dev demo: the `motd` Block matches the starter catalogue

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex, ≤ ~60 lines)   · **Depends on:** –

## Goal
Found in T-154: the catalogue Block `motd` (`blocks/motd.yaml`) has the parameter `message`; the dev-demo data (`serve --dev --dev-demo`) stores a `motd` Block and Targets that use `text`. So "Update from the catalogue" for `motd` ends with 422 in the demo. The demo must show the catalogue flow working.

## Context (read only these)
- `blocks/motd.yaml` · the dev-demo seed (search `dev-demo` / `dev.go`) · `docs/tasks/T-146-catalogue-api.md`

## Acceptance criteria
- [x] First check the cause yourself and say in the PR which side is wrong. Expected fix: the demo seed uses the catalogue Blocks (same parameter names), or a deliberately older `motd` whose update Plan is valid.
- [x] A test: in the demo seed, the update Plan from the catalogue for every stored catalogue Block validates (no 422).
- [x] The 422 for a real mismatch stays (do not weaken validation). If web e2e or README pictures depend on the old text, say so; do not regenerate pictures.
- [x] Tests: `make lint test` passes. Own worktree and branch, PR to `main`, report to `pl-lead`.

## Out of scope
- Catalogue content changes other than what the check proves wrong.

## Result (owner, max 8 lines)
branch · commit · tests · notes

pl-codex-fix/T-161-demo-motd-matches-catalogue · see PR commit
Tests: make lint test passed; make build passed; focused API test passed.
Regression: original seed returns 422 for the catalogue motd update.
Cause: demo uses text; catalogue correctly uses message. Seed and Targets now use message.
Validation unchanged; regression test checks stored catalogue updates and real mismatch 422.
Web history row matcher updated; Text field label retained; README pictures unchanged.
History e2e attempted: Chromium cannot launch (missing libatk-1.0.so.0).

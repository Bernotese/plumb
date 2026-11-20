# T-258 – Dependency leftovers before the release (release slice C2)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-257 (C3) is up

## Goal
Release slice, card **C2** of `docs/plans/release-slice.md`. The first release ships no known vulnerable dependency without a written reason.

## Acceptance criteria
- [ ] `govulncheck ./...` and `npm audit` (prod and dev, in `web/`): the list as it is on `main` today. For each finding: fixed by an update, or "does not reach our code" with the proof (govulncheck's call analysis; for npm: dev-only and not in `web/dist`), or open with a reason. GO-2026-5932 (the one the CI log names): which module, and is a fixed version out?
- [ ] Updates only where a fix needs them; no major version jump without `pl-lead`; the pinned toolchain of T-255 (Go 1.27.4, Node 26.13.0) changes only if a fix needs it – then in every place the pin test checks, and say that the RC hashes change.
- [ ] After the updates: the build is still reproducible (the dev VM build equals the CI build of the commit, as in T-255); `web/dist` has no `.map` file.
- [ ] What stays open is written for the changelog (one line each in `CHANGELOG.md` "Unreleased" → "Known issues").
- [ ] `make lint test`; PR to `main`; report with the CI job that proves it.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-255 – Release contract: version, checklist, changelog format, reproducible build check

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** slice 8 (done)

## Goal
Release slice, card **C1** (step 1) of `docs/plans/release-slice.md`. Everything a release needs as files and checks is in the repo. Nothing is released.

## Context (read only these)
- `docs/plans/release-slice.md`: section 3 (rows Version, Upgrade test, Release = the tested RC), section 5 row C1, section 5a (P3).
- `AGENTS.md`: "Autonomy and release" (D-326), "Small team" (D-438), "Git".
- `scripts/package-release.sh`, the release workflow of T-124 (read only; do not run it).

## Acceptance criteria
- [ ] The version is a **proposal** (v0.1.0) until the user answers U6: `main.version` by ldflags, `plumb --version`, `plumb-agent version`, `info.version` in `openapi.yaml` agree, and one test fails when they do not. A dev build still says what it is (commit, "dev").
- [ ] `docs/RELEASE.md`: the release checklist from the plan (steps, who does what, what the user does, how to verify a download with `SHA256SUMS` and, if U6 says yes, `gh attestation verify`). User-facing parts only as far as a user needs them; no host data.
- [ ] `CHANGELOG.md` format with the first section "Unreleased" (filled in C6).
- [ ] **Reproducibility (P3):** build the tarball of one commit on the dev VM and in a CI job of a branch (a normal PR job that uploads no release, makes no tag and no draft). Compare `SHA256SUMS`. Report: equal or not, and what differs (paths, times, Go/Node versions, the web bundle). If small fixes make it equal (`-trimpath`, `SOURCE_DATE_EPOCH`, a fixed tar order and mtime), do them; if not, say so: then C10 tests the workflow's own artifact.
- [ ] **Never:** start the release workflow, push a tag, make a GitHub release or draft, change a repo setting or secret (D-326). Decisions only with numbers from `pl-lead`.
- [ ] `make lint test`; `origin/main` merged; PR to `main`; report to `pl-lead` (name the CI job that proves it and what skips locally).

## Result (owner, max 8 lines)
branch · commit · tests · notes

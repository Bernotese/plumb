# T-124 – GitHub releases with ready binaries and checksums

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
A user gets Plumb without building it: a release holds the ready programs and their checksums (user order, 2026-11-13). **This card only prepares it. No release, no tag and no pre-release is published without the user's own yes to pl-lead.**

## Context (read only these)
- `Makefile` (build targets, version stamping), `.github/workflows/ci.yml`
- `install/install.sh` (which files it needs and how it finds them), `README.md` "Install"

## Acceptance criteria
- [ ] A workflow `.github/workflows/release.yml` that runs only on a manual start (`workflow_dispatch` with a version input); not on push, not on a tag. It builds `plumb`, `plumb-agent` (linux/amd64; arm64 only if the build already supports it), packs them with `install.sh` and what the installer needs, and writes `SHA256SUMS`
- [ ] The workflow makes a **draft** release; a person publishes it. Least rights: `contents: write` only in this job; actions pinned as in `ci.yml`; no new secret, no repo setting changed
- [ ] The build is reproducible enough that two runs of the same commit give the same checksums (`-trimpath`, fixed build id / version stamp); say what you checked
- [ ] The version in the binaries equals the release version; `plumb --version` shows it
- [ ] A dry check in CI or locally that packs the files and verifies `SHA256SUMS` without making a release
- [ ] A draft of the README "Install" text with download + checksum check, in the PR description only (pl-lead swaps the `make build` lines after the first release)
- [ ] Do not start the workflow, do not push a tag
- [ ] `make lint test build` green; small diff

## Out of scope
- Signing (cosign/GPG), package repositories, auto-update of the controller; changes to repo settings

## Result (owner, max 8 lines)
branch · commit · tests · notes

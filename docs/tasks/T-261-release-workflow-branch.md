# T-261 – Release workflow on a branch: bundle checks, release notes template, attestation step (release slice C4)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer (critical: the workflow can publish)   · **QA:** –
- **Size:** S   · **Depends on:** T-255, T-257 merged

## Goal
Release slice, card **C4** of `docs/plans/release-slice.md`. The release workflow is ready for the day the user says yes. **It is not run.**

## Acceptance criteria
- [ ] Two PRs. **PR A (may merge now):** checks that run in the normal CI on the tarball that `package-release.sh` builds: `web/dist` has no `.map` file and no test key (D-369), the bundle's `install.sh` is stamped and holds no test hook (the env list test of T-257/T-259), `SHA256SUMS` is strict; the release notes template (`docs/` or `.github/`) with the known limits (O1, O3, O5, R4) and the "how to verify" text. No change of what triggers `release.yml`.
- [ ] **PR B (a DRAFT; merges only after the user's yes to U6):** the build provenance attestation step in `release.yml` (`actions/attest-build-provenance` pinned by commit SHA, the least permissions: `id-token: write`, `attestations: write`, `contents: write` only where the draft is made), the draft-only release (never published by the workflow). Say in the PR what each permission is for.
- [ ] **Never** start `release.yml` (no `workflow_dispatch` run, no tag, no draft release), never change a repo setting, secret or environment (D-326). A test or a dry lint of the workflow (actionlint) is fine.
- [ ] Third-party actions pinned by commit SHA in both workflows; list them.
- [ ] `make lint test`; evidence in the PR first; report both PR numbers.

## Result (owner, max 8 lines)
branch · commit · tests · notes

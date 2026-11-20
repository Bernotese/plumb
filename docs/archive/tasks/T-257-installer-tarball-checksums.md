# T-257 – Installer: check the tarball's SHA256SUMS; the upgrade path from a tarball (release slice C3)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer (installer = critical)   · **QA:** C9
- **Size:** M   · **Depends on:** T-255 (C1)

## Goal
Release slice, card **C3** of `docs/plans/release-slice.md`. A user installs and upgrades from the release tarball. The installer refuses a tarball whose files do not match its `SHA256SUMS`.

## Context (read only these)
- `docs/plans/release-slice.md`: section 1 (R2), section 3 (rows Upgrade test, Release = the tested RC), section 5 row C3.
- `install/` (the installer and its tests), `scripts/package-release.sh`, `docs/RELEASE.md` (from T-255).

## Acceptance criteria
- [ ] Before any binary of the unpacked tarball is used or pushed: every file that `SHA256SUMS` lists is there and matches; a mismatch, a missing file, a file that is not listed but would be used, or a missing `SHA256SUMS` → the installer stops, names the file, changes nothing (also in `--dry-run`, which shows the check). Say in the README text what this proves (integrity, not who made it).
- [ ] The check cannot be skipped by a path trick in `SHA256SUMS` (absolute paths, `..`, a symlink in the tarball, two lines for one file, CRLF, an empty line); tests for each.
- [ ] The upgrade over an existing install from a tarball keeps data, keys, the recovery setup and the ACL lines (0 `pveum` lines when nothing changed); the dry run shows the version change old → new; a downgrade is refused or asks, as the code does today (say which).
- [ ] No new token right, no ACL change, no change of the installer's flags without `pl-lead`. Never start the release workflow, push a tag or make a release (D-326).
- [ ] Tests incl. the fake PVE; mutants of the check; `make lint test`; name the CI job that proves it and what skips locally; PR to `main`; report. The real-host proof is C7.

## Result (owner, max 8 lines)
branch · commit · tests · notes

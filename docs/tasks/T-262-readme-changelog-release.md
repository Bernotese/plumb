# T-262 – README and CHANGELOG for the first release (release slice C6)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M   · **Depends on:** T-255, T-257, T-259 (texts), T-261 PR A

## Goal
Release slice, card **C6** of `docs/plans/release-slice.md`. A new user can install from the release tarball, verify it, upgrade, and knows the rights Plumb takes and its limits. `CHANGELOG.md` has the first entry ready.

## Acceptance criteria
- [ ] README (user docs only: no decision or task numbers, no development notes, no host data; pictures only from the mock, `?as=admin`; `pl-lead` looks at each new picture): the upgrade path (incl. the one root command for guests made before slice 3, O6), the **rights table** exactly as the installer sets them (each right, the path, why; the extra bridge/VLAN and guest backup storage lines), the limits (O5 Ubuntu 26.04 stock image on DHCP until its first restart; PBS; restore of a guest that is gone; the unexplained test flake is NOT a user limit and stays out), the slice 8 features as they are on `main`.
- [ ] **Waits for the user, write both variants as marked blocks in the PR text, not in the README:** the PBS sentence (U9: "not tested against a real PBS" is the text today), the restore of a gone guest (U10: the way by hand is the text today), and the switch of the install path from "build from source" to "download the release tarball" (U6: until the yes the README keeps today's install path and has the tarball path ready in the PR).
- [ ] The rights table has a test that fails when the installer's ACL lines and the README table differ (parse the dry-run output of the fake PVE run or the installer's own list).
- [ ] `CHANGELOG.md` "Unreleased": the features by area, the known issues (T-258's line, O5), the upgrade notes. No version date (U6).
- [ ] `make lint test`; `origin/main` merged; evidence in the PR first; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

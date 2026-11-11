# T-064 – Image builds: web screens

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-065)
- **Size:** L   · **Depends on:** T-058 (mock part), T-062 (real API)   · **Parent:** T-039

## Goal
The user sees, makes and builds Images in the UI.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §7, §10; `api/openapi.yaml` Image parts; `web/src/lib/mock/mock.ts`

## Acceptance criteria
- [ ] Routes `/images`, `/images/new`, `/images/:name`, `/images/:name/edit`. List: name, base, built or stock, newest version, last build, next build (local time). Detail: versions with state, date, Run link, Targets from each version; recipe; "Build now"; unused import files with the `pvesm free` line; storage use.
- [ ] Form: base, build on/off, Blocks without secret parameters, upgrade, build guest, keep, schedule (day/week/month + time).
- [ ] Plan screen: Image part (new version, resources, "removes v1", "A person must approve: image recipe"). Run screen: build steps. Target screen: "Image <name> v<N>". Target form: smallest disk check.
- [ ] Mock first; then the real API after T-062.
- [ ] `browser-shot` light and dark of each new screen; Playwright on `plumb serve --dev` (fake driver, simulated `seal`).
- [ ] Tests: web unit + e2e pass.

## Out of scope
- LXC images.

## Result (owner, max 8 lines)
branch · commit · tests · notes

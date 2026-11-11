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
- [x] Mock first; then the real API after T-062.
- [x] `browser-shot` light and dark of each new screen; Playwright on `plumb serve --dev` (fake driver, simulated `seal`).
- [ ] Tests: web unit + e2e pass.

## Out of scope
- LXC images.

## Result (owner, max 8 lines)
- Part 1 (mock screens): on main. Part 2 (real API, branch `pl-frontend/T-064-web-images-real` on `pl-architect/T-062b-build-lane`): the HTTP client already used the T-062 routes, and the generated types check clean against them.
- Image page: "Left templates" (`ImageStatus.left_templates`) with name, VMID and "left by a build; remove it by hand"; no command, no Copy (a command needs pl-lead's go and a strict form). Shown only when the list is not empty.
- `plumb serve --dev`: each running build guest gets a simulated agent (linktest) that seals and powers the guest off, like the real agent (T-060). `TestDevAgentForNewTarget` builds an Image in dev mode (v1 `ready`, the agent stops after).
- e2e `slice2-images.spec.ts`: form → Plan with "image recipe" → Run with `image:seal` → v1 ready → "Build now" → v2 ready.
- Tests: `npm run lint`, `npm test`, all 9 e2e specs, `go test ./cmd/plumb` ok. Screens: light, dark, 390 px. Seen at 390 px: the top navigation overflows by about 20 px (not from this card).

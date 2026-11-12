# T-081 – web: LXC Images and Targets

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** T-075
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
The UI can make an `lxc` Image and an LXC Target and shows the unused template files with the `pvesm free` line.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §8
- `web/`

## Acceptance criteria
- [ ] Review N1 of PR #154: `ImageView` shows "sha256 undefined" for an `lxc` Image and `ImageForm` is VM-only; both handle `lxc` (SHA-512).
- [ ] Everything in §8 of the plan. Screenshots with `browser-shot` (light and dark).
- [ ] Tests: `make lint test build` passes.
- [ ] New decisions go to `docs/decisions/T-081.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- Build section for `lxc` (T-082).

## Result (owner, max 8 lines)
`pl-frontend/T-081-web-lxc` · `make lint test build` → ok; `npx playwright test` → 10 passed (new `slice2-lxc.spec.ts`); vitest 363 + new `lxc.test.ts`.
- Image form: type radio (VM / Container), for lxc: http allowed, SHA-512, pick list of the two known templates (pins = ARCHITECTURE table, a test keeps them equal), no build section. Edit: type fixed. Image page: type, `sha512 …` (N1 of #154), "Unused template files" with the `pvesm free …vztmpl/plumb-ct-…tar.gz` line (Copy only for Plumb's name form), also without build.
- Target form: type follows the Image, no `user`, "SSH keys for root", no "Restart needed", container wording (allow_restart help, re-enroll texts). Images list: "Container". Plan page: the image-recipe reason also names container images (D-235).
- Mock: `debian-ct` Image and `ct1` Target; image-recipe for lxc as D-235.
- Screenshots: `docs/screenshots/T-081-lxc-*.png` (light, dark, 390 px). No new decision.

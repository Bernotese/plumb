# T-049 – Web: show `after` and the cause of a restart

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-046   · **Parent:** T-044

## Goal
The Plan view shows what a resource waits for and what causes a restart; the mock knows `after` and `restarts`.

## Context (read only these)
- `docs/tasks/T-044-block-order-nginx-site.md` – Plan sections 2, 5, 7
- `docs/decisions/T-044.md` – D-171, D-172
- `api/openapi.yaml` – `ResourcePlan.after`, `ResourcePlan.restart_by`, `StepStatus`
- `web/src/screens/PlanView.svelte`, `web/src/screens/RunView.svelte`, `web/src/lib/mock/mock.ts` (`render`, `planTarget`)

## Acceptance criteria
- [ ] Plan view: a resource with `after` shows "after `package:nginx`". A service with `restart_by` shows "restart because of `<ids>`". No new sort, no grouping.
- [ ] Run view: the output of a skipped step is visible (`not applied: package:nginx was failed`). Component test with a fixture; the mock has no failed Runs.
- [ ] `mock.ts`: a seeded `nginx-site` Block with `after: [package:nginx]` and `restarts: [service:nginx]`; `render` merges `restarts` into `restart_on` before the overrides and sets `after` on the first resource of the use; an `after` id that is not on the Target is a 422 with the text of Plan section 2; `planTarget` fills `after` and `restart_by`. No cycle check in the mock.
- [ ] Target form: no change.
- [ ] Tests: mock render tests, Plan view tests for both labels; `npm run lint` and `npm test` pass.
- [ ] `browser-shot` of a Plan with `nginx` + two sites, light and dark, in `docs/screenshots/`.

## Out of scope
- A Block field on Plan resources, a view of Blocks, raw mode.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-044 – Order between Block uses (`after`), then `nginx-site`

- **Owner:** pl-architect (plan first), then tbd   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-020 on `main`   · **State:** plan ready (2026-11-10), waits for the user's approval. No feature code and no contract edit before that, because the plan changes the agent

## Goal
A Block use can depend on another Block use of the same Target, so a Block such as `nginx-site` applies clean in the first Run on a new Target.

## Situation
- The agent runs the Block uses of a Target in parallel (D-027). `nginx-site` needs `/etc/nginx/sites-available` from `package:nginx` of the `nginx` Block, so it failed at the first apply. It left PR #73 for that reason (D-165: a starter Block must apply clean in the first Run on a new Target).
- `spec.restarts` (first D-165 draft, pl-architect comments on PR #73) left PR #73 too. It belongs here.
- "Each use waits for the uses before it" was refused: it makes every Target sequential, changes the skip rule after a failure, and makes list position a hidden contract.

## Context (read only these)
- pl-architect comments on PR #73 (issuecomment-6775293986, issuecomment-6775338537), pl-reviewer review of PR #73
- `docs/ARCHITECTURE.md` §10 (roadmap: `after`, `uses`), `docs/DECISIONS.md` D-027, D-165
- `internal/agent/exec.go`, `internal/state` (render, validate)

## Acceptance criteria (refined by the plan; final after the user's approval)
- [ ] A Block names the resources it comes after (`spec.after`, D-167). Only these edges are sequential; everything else stays parallel.
- [ ] An unknown id, an id of the same use and a cycle are save-time errors (D-168). The Plan shows `after` on the resource that waits (D-171).
- [ ] Skip rule (D-170): a use whose `after` resource ended `failed`, `refused` or `skipped` is skipped and names it; other uses run. Tested in `internal/agent`.
- [ ] `nginx-site` and `spec.restarts` are back (D-172, D-173). One apply command on a guest without `/etc/nginx`, with `nginx` and two `nginx-site` uses, in both Target orders: no step fails, and each site answers with its own page.
- [ ] That test (`TestGuestNginxSitesFirstRun`) runs in the CI guest job (D-174). `make test`, `make lint` and `sg docker -c "make test-guest"` pass.

## Out of scope
- `when`, `each`, `uses`, typed values (other roadmap template features).
- `after` on a Block use in the Target, Block names in `after`, templates in `after` / `restarts`, reverse order for removal (D-175).

## Plan (architect)
Decisions: `docs/decisions/T-044.md`, D-167 … D-175, all PROPOSED. Nothing below is built yet.

### 1. The dependency (D-167)
```yaml
kind: Block
name: nginx-site
spec:
  after: [package:nginx]       # each use of this Block waits for this resource
  restarts: [service:nginx]    # changes of this use restart this service (section 5)
  resources: [...]
```
- **Where:** in the Block, `spec.after`. The Target does not change. The user adds the Block and writes no order.
- **Name:** a resource id (`type:key`), as in `restart_on`. Literal strings, no templates, at most 16.
- **Meaning:** the first resource of each use of the Block starts only after every named resource ended. The other resources of the use follow in the listed order, as today.
- **Everything else stays parallel.** Two `nginx-site` uses run side by side. List position has no meaning.
- **Why not a Block or use name (`after: [nginx]`):** that means "after the last step of `nginx`". The last step is `service:nginx`. With `restarts`, `service:nginx` waits for the site. Site waits for service, service waits for site: a cycle. The site needs only the directory that `package:nginx` creates.

### 2. Save-time errors (D-168)
| Case | Where | Text |
|---|---|---|
| Type of an id is not an agent resource type | Block, `/spec/after/<n>` | `after: "<id>" has no known resource type` |
| Id is not on the Target | Target, `/spec/blocks/<i>` | `Block "nginx-site" (use "site-a") comes after package:nginx, but package:nginx is not on this target; add the Block that provides it` |
| Id belongs to the same use | Target, `/spec/blocks/<i>` | `Block "x" (use "x") comes after file:/a, which is its own resource; resources of one Block already apply in the listed order` |
| Cycle | Target, `/spec/blocks` | `apply order has a cycle: <id> (use "a") waits for <id> (use "b"), which waits for it; check after, restarts and restart_on` |
- One new function in `internal/wire` builds the wait graph (step before in the same use, `restart_on`, `after`) and finds duplicates and cycles. `state.Render` and `agent.plan` both call it.
- Side effect: a `restart_on` cycle (today an apply error, D-055) becomes a save error too. `state.Load` refuses such a state. No starter Block has one.

### 3. Wire, contract, schema, migration (D-169, D-171)
- `wire.Resource.After []string` (`json:"after,omitempty"`). Render sets it on the first resource of a use. `ApplyBody`, `Planned` and the reports do not change.
- `internal/state/schema/block.json`: `spec.after` (strings, pattern `^[a-z][a-z0-9_-]*:.+$`, `uniqueItems`, `maxItems: 16`) and `spec.restarts` (pattern `^service:.+$`, same limits).
- `api/openapi.yaml`: `ResourcePlan.after` (optional list of ids); text of `StepStatus` `skipped`; then `web/src/lib/api.gen.ts` and the examples.
- **No migration.** The Plan is a JSON blob. `run_step` keeps its columns.
- An old agent ignores `after` and runs in parallel as before. No version gate (open question 3).

### 4. Agent execution and skip rule (D-170)
- `agent.plan` gets the graph from `wire`. A step waits for its `after` steps before it observes.
- All `after` steps ended `unchanged` or `applied` → the step runs.
- One ended `failed`, `refused` or `skipped` → the step is `skipped`, output `not applied: package:nginx was failed`. The rest of its use is skipped by the rule that exists. So a skip passes down a chain.
- Uses with no relation to the failure run to the end.
- `restart_on` does not change: the service waits, and it still runs when a listed resource failed. A broken site does not stop Nginx for the other sites.
- An `after` id that is not in the list fails the apply before anything runs.
- `linktest` fake agent (also `serve --dev`): no change. It applies in list order, in memory, and has no skip rule today.

### 5. `spec.restarts` (D-172)
As in the first D-165 draft (PR #73, issuecomment-6775293986). Render adds the ids of the use to `restart_on` of each named service, after the Blocks and before the overrides. The agent sees a normal `restart_on`. The Plan shows the restart.

### 6. `nginx-site` returns (D-173)
- `blocks/nginx-site.yaml` from commit 859eddf, plus `after: [package:nginx]`, `restarts: [service:nginx]`. Remove the override text from the description. `blocks/nginx.yaml` gets "Add more sites with the nginx-site Block." again.
- First Run on a new Target: `package:nginx` → default site, site A, site B in parallel → `service:nginx` restarts once.
- Without the `nginx` Block the save fails (row 2 of the table).

### 7. How the user sees it (D-171)
- Plan view: the resource that waits shows "after `package:nginx`". The list order stays.
- Run view: a skipped step shows `not applied: … was failed` in the output column. No new column.
- Target form: no change.

### 8. Test plan (D-174)
- `internal/wire`: graph function: order, duplicate, cycles (`after`↔`after`, `after` + `restart_on`), unknown `after` id.
- `internal/state`: schema cases; `After` only on the first resource; each row of the error table; `restarts` (order, two uses, override replaces the list, missing service); negative: `after: [service:nginx]` + `restarts: [service:nginx]` is a cycle.
- `internal/agent`: dependency ends before the dependent starts; a use with no relation runs while the dependency blocks; dependency `failed` / `refused` / `skipped` → the whole dependent use is `skipped` with the output; chain of three uses; service with `restart_on` on a skipped resource still runs; both list orders. Update `TestPlanOrder`.
- `blocks`: `nginx-site` in `TestBlocks`; render `nginx` + two sites in both orders (6 ids in `restart_on`, `after` on the two site files); save without `nginx` fails.
- `internal/api`: contract test with `ResourcePlan.after`; a new-Target Plan carries it.
- `test/slice` (no Docker): the fixture makes `/etc/nginx` only when the fake package install runs, with a delay. `nginx` + two sites succeed in the first Run. Mutation: without `after` in the Block the test fails.
- **Guest test, CI guest job:** `TestGuestNginxSitesFirstRun` in `internal/agent` (tag `guest`, `run.sh` already runs `^TestGuest`). Purge nginx, check that `/etc/nginx` is gone, render the Target from the real `blocks/*.yaml` (new `blocks/embed.go` with `blocks.FS`), one `apply` with all resources `unverified`. Expect no `failed`, `refused`, `skipped`; `curl -H "Host: a.lan"` and `b.lan` return each site's own page (the test makes two web roots first). Run it for both Target orders. Raise `-test.timeout` of `agent.test` in `run.sh` (1m is too short for two installs). Local: `sg docker -c "make test-guest"`.
- Web: mock render tests, Plan view label test, `browser-shot` of a Plan with `nginx` + two sites (light, dark).

### 9. Child cards (after the user's approval)
| Card | Owner | Size | Work | Needs |
|---|---|---|---|---|
| T-046 contract | pl-architect | S | `openapi.yaml`, `block.json`, `wire.Resource.After`, the graph function in `wire` with tests, `api.gen.ts`, ARCHITECTURE §3 and §10, comments that say "Blocks run in parallel" | approval |
| T-047 state + plan | pl-backend | M | `validate.go`, `render.go` (`after`, `restarts`, checks, cycle), `ResourceChange.After` → API view, `nginx-site` in the dev seed | T-046 |
| T-048 agent | pl-backend (or pl-frontend if free) | S–M | `agent.plan` uses the graph; wait, skip rule, skip output; agent tests | T-046 |
| T-049 web | pl-frontend | M | `mock.ts` (`after`, `restarts`, unknown-id error, `nginx-site`), Plan view label, skip output check, screenshots | T-046 |
| T-050 Block | pl-codex-fix (`codex-usage --route`) | S | `blocks/nginx-site.yaml`, `blocks/embed.go`, `blocks` tests, `nginx.yaml` description | T-047 |
| T-051 QA | pl-codex-qa | M | guest test, slice fixture test, agent skip-rule audit, mutations | T-047, T-048, T-050 |
- T-047, T-048 and T-049 run in parallel. pl-reviewer reviews T-047 and T-048 (focus: agent, skip rule). T-044 closes when T-051 is green in CI.
- The mock does not get the cycle check. The server has it.

### 10. Open questions for the user
1. **Name form.** `after` names a resource (`package:nginx`), not a Block (`nginx`). The card said "another Block use". **Recommendation: resource id.** A Block name makes a cycle with `restarts` (section 1).
2. **Order in the Target.** Can a user also write `after` on a Block use in a Target? **Recommendation: not now.** No Block needs it, and the form has no place for it. It can come later without a break (D-175).
3. **Old agents.** An agent from before this change ignores `after`. **Recommendation: no version gate now.** No agent runs on a real host yet. The gate comes with agent self-update.
4. **Plan view.** A label on the waiting resource, or a new sort of the Plan in apply order? **Recommendation: label only.** Parallel work has no single order.
5. **Failed site and Nginx.** When one site fails, Nginx still restarts for the others (`restart_on` rule, as today). When `package:nginx` fails, all sites are skipped. **Recommendation: accept both.**

## Result (owner, max 8 lines)
branch · commit · tests · notes

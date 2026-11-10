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
Plan review: pl-reviewer, PR #77 issuecomment-6781495903 (approve; S1 … S5 and N1 … N3 are in this text).

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
- The graph function returns each edge with its kind: step before in the same use, `restart_on`, `after` (S1).
- Side effect: a `restart_on` cycle (today an apply error, D-055) becomes a save error too. No starter Block has one.
- **A stored state with a cycle (S3):** the cycle check runs for each proposed state (proposal, rollback, import). `state.Load` of a stored revision does not run it. So the controller starts and serves as before, and it logs a warning with the Target name. The applies of that Target fail at the agent, as today. The next proposal is refused with the cycle text until it repairs the cycle. Reason: `Load` runs before every proposal. If `Load` refused the state, nobody could save the repair.

### 3. Wire, contract, schema, migration (D-169, D-171)
- `wire.Resource.After []string` (`json:"after,omitempty"`). Render sets it on the first resource of a use. `ApplyBody`, `Planned` and the reports do not change.
- `internal/state/schema/block.json`: `spec.after` (strings, pattern `^[a-z][a-z0-9_-]*:.+$`, `uniqueItems`, `maxItems: 16`) and `spec.restarts` (pattern `^service:.+$`, same limits).
- `api/openapi.yaml`: `ResourcePlan.after` and `ResourcePlan.restart_by` (optional lists of ids, section 7); text of `StepStatus` `skipped`; then `web/src/lib/api.gen.ts` and the examples.
- `wire.StatusSkipped` comment: it now also means "an `after` resource did not succeed" (N3).
- **No migration.** The Plan is a JSON blob. `run_step` keeps its columns.
- An old agent ignores `after` and runs in parallel as before. No version gate (open question 3).

### 4. Agent execution and skip rule (D-170)
- `agent.plan` gets the graph from `wire`. A step keeps two lists: `needs` (`after` edges) and `restartDeps` (`restart_on` edges). Only `restartDeps` can cause a restart (S1). Today both would share `step.after`; then a service that is the first resource of a use would restart when its `after` resource changed.
- A step waits for its `needs` before it observes.
- All `after` steps ended `unchanged` or `applied` → the step runs.
- One ended `failed`, `refused` or `skipped` → the step is `skipped`, output `not applied: package:nginx was failed`. The rest of its use is skipped by the rule that exists. So a skip passes down a chain.
- Each skipped step gets an output that names the cause, also after a failure inside its own use (N1). No empty skipped rows in the Run view.
- The context ends during the wait (timeout, stop) → the step and the rest of its use are `skipped`, output `not applied: the apply ended while this resource waited for package:nginx`. `apply` returns and leaves no goroutine (S5).
- Uses with no relation to the failure run to the end.
- `restart_on` does not change: the service waits, and it still runs when a listed resource failed. A broken site does not stop Nginx for the other sites.
- An `after` id that is not in the list fails the apply before anything runs. (A normal Run always sends the full render, so this guards against a controller bug only.)
- `after` is order only. An override that sets the named resource to `state: absent` passes the save. The Plan shows the removal, and the dependent can fail at the apply. No check for that (N2).
- `linktest` fake agent (also `serve --dev`): no change. It applies in list order, in memory, and has no skip rule today.

### 5. `spec.restarts` (D-172)
As in the first D-165 draft (PR #73, issuecomment-6775293986). Render adds the ids of the use to `restart_on` of each named service, after the Blocks and before the overrides. The agent sees a normal `restart_on`. The Plan shows the restart.
- **S2:** the merged list stays `[]any` of strings. The render check (`render.go`) and `plan.plannedRestarts` cast `restart_on` to `[]any`; with `[]string` the check sees nothing and the planned restart of D-160 is lost. One helper, `wire.RestartOn`, reads the list in render, `plannedRestarts`, `resource.RestartChange` and the agent.
- Security (review): `restarts` can name any service, for example `service:ssh`. That is no new power, each restart is `disruptive` and in the Plan. The Plan shows the cause (section 7). The `^service:` pattern and `maxItems: 16` stay.

### 6. `nginx-site` returns (D-173)
- `blocks/nginx-site.yaml` from commit 859eddf, plus `after: [package:nginx]`, `restarts: [service:nginx]`. Remove the override text from the description. `blocks/nginx.yaml` gets "Add more sites with the nginx-site Block." again.
- First Run on a new Target: `package:nginx` → default site, site A, site B in parallel → `service:nginx` restarts once.
- Without the `nginx` Block the save fails (row 2 of the table).

### 7. How the user sees it (D-171)
- Plan view: the resource that waits shows "after `package:nginx`". The list order stays.
- Plan view: a service with a planned restart shows "restart because of `file:/etc/nginx/sites-available/a`" (`ResourcePlan.restart_by`). The controller takes the ids from `restart_on` that change in this Plan or are left out of the observe (D-156). Display only; `wire.Covers` does not use it.
- Run view: a skipped step shows `not applied: … was failed` in the output column. No new column.
- Target form: no change.

### 8. Test plan (D-174)
- `internal/wire`: graph function: order, duplicate, cycles (`after`↔`after`, `after` + `restart_on`), unknown `after` id.
- `internal/state`: schema cases; `After` only on the first resource; each row of the error table; `restarts` (order, two uses, override replaces the list, missing service, a bad id in the merged list still fails the `restart_on` check); negative: `after: [service:nginx]` + `restarts: [service:nginx]` is a cycle. S3: a stored revision with a `restart_on` cycle loads; a proposal that does not repair it is refused and names the Target; a proposal that repairs it passes.
- `internal/agent`: dependency ends before the dependent starts; a use with no relation runs while the dependency blocks; dependency `failed` / `refused` / `skipped` → the whole dependent use is `skipped` with the output; chain of three uses; service with `restart_on` on a skipped resource still runs; both list orders. S1: a service is the first resource of a use with `after: [package:x]`, `package:x` is applied, no restart. S5: the dependency blocks, the context ends, the dependent use is `skipped` with the output, `apply` returns, no goroutine leaks. N1: every skipped step has an output. Update `TestPlanOrder`.
- `blocks`: `nginx-site` in `TestBlocks`; render `nginx` + two sites in both orders (6 ids in `restart_on`, `after` on the two site files); save without `nginx` fails.
- `internal/api`: contract test with `ResourcePlan.after` and `restart_by`; a new-Target Plan carries `after`. S2: an existing Target with `nginx` gets a site with a new secret binding (a left-out resource); `service:nginx` still gets the planned restart, with `restart_by`.
- `test/slice` (no Docker): the fixture makes `/etc/nginx` only when the fake package install runs, with a delay. `nginx` + two sites succeed in the first Run. Mutation: without `after` in the Block the test fails. This test is the proof of the race; the guest test depends on apt timing. Two paths: (a) a new Target, all `unverified`; (b) S4: an enrolled Target without nginx adds `nginx` + two sites, so the Plan has real planned changes and `wire.Covers` must match after the wait.
- **Guest test, CI guest job:** `TestGuestNginxSitesFirstRun` in `internal/agent` (tag `guest`, `run.sh` already runs `^TestGuest`). Purge nginx, check that `/etc/nginx` is gone, render the Target from the real `blocks/*.yaml` (new `blocks/embed.go` with `blocks.FS`), one `apply` with all resources `unverified`. Expect no `failed`, `refused`, `skipped`; `curl -H "Host: a.lan"` and `b.lan` return each site's own page (the test makes two web roots first). Run it for both Target orders. Raise `-test.timeout` of `agent.test` in `run.sh` (1m is too short for two installs). Local: `sg docker -c "make test-guest"`.
- Web: mock render tests, Plan view tests for the `after` label and the restart cause, `browser-shot` of a Plan with `nginx` + two sites (light, dark).

### 9. Child cards (after the user's approval)
| Card | Owner | Size | Work | Needs |
|---|---|---|---|---|
| T-046 contract | pl-architect | S | `openapi.yaml` (`after`, `restart_by`, `skipped` text), `block.json`, `wire.Resource.After`, the graph function in `wire` (edges with kind) and `wire.RestartOn` with tests, `StatusSkipped` comment, `api.gen.ts`, ARCHITECTURE §3 and §10 (also: agent self-update owns the version gate), comments that say "Blocks run in parallel" | approval |
| T-047 state + plan | pl-backend | M | `validate.go`, `render.go` (`after`, `restarts` as `[]any`, checks, cycle for proposed states only, `Load` warning), `wire.RestartOn` in render, `plannedRestarts`, `RestartChange`; `ResourceChange.After` and `RestartBy` → API view; `nginx-site` in the dev seed; tests S2, S3 | T-046 |
| T-048 agent | pl-backend (or pl-frontend if free) | S–M | `agent.plan` uses the graph; `needs` apart from `restartDeps`; wait, skip rule, output on every skipped step, context end during the wait; agent tests S1, S5, N1 | T-046 |
| T-049 web | pl-frontend | M | `mock.ts` (`after`, `restarts`, `restart_by`, unknown-id error, `nginx-site`), Plan view: `after` label and restart cause, skip output check, screenshots | T-046 |
| T-050 Block | pl-codex-fix (`codex-usage --route`) | S | `blocks/nginx-site.yaml`, `blocks/embed.go`, `blocks` tests, `nginx.yaml` description | T-047 |
| T-051 QA | pl-codex-qa | M | guest test, slice fixture test with both paths (new Target, existing Target: S4), agent skip-rule audit, mutations | T-047, T-048, T-050 |
- T-047, T-048 and T-049 run in parallel. pl-reviewer reviews T-047 and T-048 (focus: agent, skip rule). T-044 closes when T-051 is green in CI.
- The mock does not get the cycle check. The server has it.

### 10. Open questions for the user
1. **Name form.** `after` names a resource (`package:nginx`), not a Block (`nginx`). The card said "another Block use". **Recommendation: resource id.** A Block name makes a cycle with `restarts` (section 1).
2. **Order in the Target.** Can a user also write `after` on a Block use in a Target? **Recommendation: not now.** No Block needs it, and the form has no place for it. It can come later without a break (D-175).
3. **Old agents.** An agent from before this change ignores `after`. **Recommendation: no version gate now.** No agent runs on a real host yet. The gate comes with agent self-update; T-046 writes that into ARCHITECTURE §10.
4. **Plan view.** A label on the waiting resource, or a new sort of the Plan in apply order? **Recommendation: label only.** Parallel work has no single order. The reviewer also asked: show which use caused a restart. **Yes, in a small form:** the Plan names the resources that cause the restart (`restart_by`). The id shows the cause, for example a site file that restarts `service:ssh`. No use id: that needs a Block field on every Plan resource, and no other view needs it.
5. **Failed site and Nginx.** When one site fails, Nginx still restarts for the others (`restart_on` rule, as today). When `package:nginx` fails, all sites are skipped. **Recommendation: accept both.** Limit: a site file that Nginx cannot read can stop Nginx for all sites at the restart. The parameters have patterns, so this is not probable. A config test before the restart is a different feature.

## Result (owner, max 8 lines)
branch · commit · tests · notes

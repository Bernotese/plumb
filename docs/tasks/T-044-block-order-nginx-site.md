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

## Result (owner, max 8 lines)
branch · commit · tests · notes

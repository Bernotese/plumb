# T-044 – Order between Block uses (`after`), then `nginx-site`

- **Owner:** pl-architect (plan first), then tbd   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-020 on `main`   · **State:** planning (user go for the plan: 2026-11-10); the plan needs the user's approval before any feature code, because it changes the agent

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

## Acceptance criteria (draft; the plan refines them)
- [ ] A Block (or a Block use) names what it comes after. Only named dependencies are sequential; everything else stays parallel.
- [ ] Cycles and unknown names are save-time errors. The Plan shows the order.
- [ ] The skip rule after a failed dependency is defined and tested.
- [ ] `nginx-site` and `spec.restarts` come back. The reviewer's one-command test passes: a new Target with `nginx` and two `nginx-site` uses applies clean in the first Run, and both sites answer.
- [ ] Guest test in the CI guest job.

## Out of scope
- `when`, `each`, `uses`, typed values (other roadmap template features).

## Plan (architect)

## Result (owner, max 8 lines)
branch · commit · tests · notes

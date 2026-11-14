# T-178 – plumb mcp: MCP server over the API

- **Owner:** pl-reviewer (implementer)   · **Reviewer:** pl-architect   · **QA:** pl-codex-qa (T-186)
- **Size:** M   · **Depends on:** T-173 merged

## Goal
Slice 6 part A, child card **A5** of T-172. An MCP client (the user's own AI client) can read, plan and comment through Plumb with a `plan` token (BRIEF §5.9, D-364). No AI call from Plumb itself.

## Context (read only these)
- Plan: `docs/tasks/T-172-slice-6-plan.md` section "Plan" (until PR #272 is merged: `git show origin/pl-architect/T-172-slice-6-plan:docs/tasks/T-172-slice-6-plan.md`), decisions D-360 … D-376 (`docs/decisions/T-172.md` on that branch), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §18 (on main). Read row A5 of the child card table, the contracts, the risks and the decisions the row names.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row A5 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] stdio only, `MaxLineLength` set; the tool list is the fixed set of ARCH §18: no approve, no ignore, no secret, no user or token tool, for any token tier. Guest-reported text reaches the client only in marked data fields (injection rule of §18).
- [ ] The MCP server is a client of the HTTP API with the user's token: it has no other way into the store. CI test with an MCP client against `serve --dev`. New dependency: exact version pinned, govulncheck clean.
- [ ] A card that adds an API operation updates `web/src/lib/roleHints.ts`, the tier test and the contract marks in the same PR. A new resource type or attribute gets the opaque/guest-value classification test (D-359).
- [ ] No new token right. New decisions only with numbers from `pl-lead`, in `docs/decisions/T-178.md`.
- [ ] Tests: `make lint test` passes; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock (D-375); report to `pl-lead`.

## Out of scope
- Everything of other rows. Part B (waits for the user). Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

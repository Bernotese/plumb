# T-236 – What is left until the release, and the plan of slice 8

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer (anything with rights, secrets, auth) + pl-backend (the rest)   · **QA:** –
- **Size:** L (plan only, no code)   · **Depends on:** –

## Goal
Slice 7 is built and accepted on the real host (T-225, three parts; README in PR #357). By D-326 the next slice is planned at once. Several points of the old slice 8 are done already (MCP server in slice 6, the Plan's document changes in T-170). So first: an honest list of what `docs/BRIEF.md` §5/§6 still asks and the code does not do, then the plan of the next slice.

## Context (read only these)
- `docs/BRIEF.md` §5, §6 · `docs/plans/ROADMAP.md` · `docs/DECISIONS.md` D-326, D-377 … D-420 · `docs/tasks/T-225.log.md` (observations) · the review comments of #321 … #358 (open notes)

## Acceptance criteria
- [ ] `docs/plans/ROADMAP.md`: slice 7 rows done. A fresh gap sweep of BRIEF §5.1 … §5.9 and §6 against `main` (do not trust the old rows: check each line of the brief against code, one row per gap, with the file or route that proves "done" for the others). Sub-agents for the wide search are fine.
- [ ] Open points from slice 7 placed or dropped with a reason: deleting an Image document leaves its templates and files in Proxmox (T-225); safety backups pile up after failed restores; an Issue resolved by the system without a comment (Issue 54); `e.restored` kept only in memory (a controller stop mid-restore loses the follow-up Plan); the old-list LXC backups; `vmbr9` on `pve-dev` (undo card or keep for later slices: say which); every low note of the reviews #321 … #358 that is not done.
- [ ] AI help on Issues (BRIEF §5.8 "Later", U5 = MCP only, D-381): say what is still missing beyond the MCP tools that exist (e.g. an "explain" prompt/resource, guidance text, a documented client workflow) and whether it needs code at all.
- [ ] The plan of the next slice in this card, same form as T-215 (scope, contracts, child cards with owner, reviewer per D-394, size, dependencies, shared files with an order, migrations "next free at merge time", risks, real-host acceptance, the rules of D-413 plus the lessons of slice 7: a restore/backup path is proven with a damaged input on the real host; two PRs on one area are refreshed against main before the second merge; local green is not proof where tests skip).
- [ ] If what is left fits one slice: say so and name what the release slice (old slice 9) then holds. **USER points** at the top (D-326 (4)): U6 release steps, U7 repair run of the user's own install, U8 bridges/tags/storage, the restore of a guest that is gone (`Datastore.Allocate`), and anything new. Never assume an answer.
- [ ] Decisions with your recommendation in `docs/decisions/T-236.md` (numbers D-421 … D-435). PR to `main` (docs only); report.

## Out of scope
- Code. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

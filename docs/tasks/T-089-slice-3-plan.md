# T-089 – Plan for slice 3

- **Owner:** pl-architect   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** M (plan only, no code)   · **Depends on:** –

## Goal
The user wants to go on. Propose what slice 3 holds, as a plan the user can approve. No code is written before the user approves.

## Context (read only these)
- `docs/BRIEF.md` §5 (what Plumb must do) and §6 (open questions) – find what is still missing after slices 1 and 2
- `docs/STATUS.md` – what is done, known limits
- `docs/ARCHITECTURE.md`, `docs/DECISIONS.md` – only the parts you need (use sub-agents for wide searches)
- Known leftovers that must each get a clear "in slice 3 / later / drop" with a reason:
  - two Images with the same base upload the repacked file twice
  - T-087 leftovers: 2 low `npm audit` findings (`deepmerge`, dev-only), GO-2026-5932 (module-only)
  - T-084: no proof with a real Image build Plan that the thin-pool metadata note shows
  - web mutation audit not done; boundary test for Content-Length == cap
  - the known Ubuntu 26.04 limit ("Open for the user" in STATUS)
  - old open Issues 14/15 on the test controller; stale worktrees (housekeeping, lead)

## Acceptance criteria
- [ ] Section "Plan" in this card: a gap list BRIEF §5/§6 vs. `main` (short table: item, state, proof)
- [ ] A proposed scope for slice 3: 1 theme or a few, with the reason for the order, and what stays for slice 4
- [ ] Child cards as a list (title, owner, size, depends on, parallel or not); no card files yet
- [ ] Risks and what needs the real test host
- [ ] "Questions for the user": at most 6, each with options and ONE recommendation and a one-line reason
- [ ] New decisions as proposals only (numbers from D-256; status "proposed")
- [ ] The whole plan is short: the user part fits one screen

## Out of scope
- Any code, migration, API or installer change
- New token rights (D-081): only as a question for the user

## Plan (architect, optional)

## Result (owner, max 8 lines)
branch · commit · tests · notes

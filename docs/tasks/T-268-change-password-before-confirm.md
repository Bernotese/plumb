# T-268 – A must-change session cannot confirm a restore or enable a token

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer (auth and sessions = critical)   · **QA:** –
- **Size:** S   · **Depends on:** #397 merged (README text)

## Goal
A session whose password must still be changed (the shown-once password after an install or a restore, a one-time password) may call `POST /restore/confirm` and `POST /tokens/{id}/enable`: the access level `human` has `duringReset: true` since T-011, from before restores existed, and only these two routes use it. So a password that was never changed can end restored mode and bring back disabled tokens. D-107 and D-321 refuse sensitive acts until the password is changed.

## Decision D-441 (pl-lead, on pl-reviewer's recommendation; write it to `docs/decisions/T-268.md`)
A must-change session can do nothing but the `/session` operations (change the password, log out, read itself). The restore confirm and the token enable need a session whose password was changed.

## Acceptance criteria
- [ ] `human` without `duringReset`; the answer for a must-change session on both routes is the same refusal the other routes give (say the code), and the web leads the user to the password change first (the restore confirm page and the token page show why).
- [ ] A test per route (must-change → refused; after the change → works); the access table test lists no route with `duringReset` outside `/session`; a mutant.
- [ ] README (the restore section of T-267) and `docs/RELEASE.md` if it names the order: first set a new password, then confirm the restore, then enable users and tokens.
- [ ] `make lint test`; evidence in the PR first; PR to `main`; report. The host proof is the R1 repeat with the final RC.

## Result (owner, max 8 lines)
branch · commit · tests · notes

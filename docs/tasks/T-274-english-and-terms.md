# T-274 – English only, one vocabulary (cleanup point 5)

- **Owner:** pl-backend (after T-271)   · **Reviewer:** pl-reviewer   · **QA:** the CI jobs
- **Size:** M   · **Depends on:** T-271

## Goal
User order D-442, point 5. Code comments, identifiers, log and error texts, UI texts, test names, script names and the docs in the repo are English, with the terms of `docs/BRIEF.md` §3.

## Acceptance criteria
- [ ] A scan for German or mixed text (a word list + umlauts + common German words in comments, strings, identifiers, file names, commit-independent docs); the list of hits in the PR; each fixed or named as a false positive (a German example value in a test of the locale handling is fine: say so).
- [ ] The vocabulary of BRIEF §3: one term per thing in the UI, the API, the docs and the error texts (for example Target, Block, Image, Plan, Run, Issue, drift, re-enroll, guest backup, safety backup). A table of the terms and the variants found; variants fixed where it changes no API field, no stored value and no file a user's install reads.
- [ ] **Do not rename** an API field, a JSON key, a DB column, a config key, a CLI flag, an installer flag, an Issue kind or an error code: these are the contract (list where a name is odd; `pl-lead` decides, after the release). A changed UI or error text that a test or the README quotes is changed there too.
- [ ] Small PRs by area (Go comments and logs; web texts; scripts and tests; docs); `make lint test`, green CI; evidence first.
- [ ] No behaviour change.

## Result (owner, max 8 lines)
branch · commit · tests · notes

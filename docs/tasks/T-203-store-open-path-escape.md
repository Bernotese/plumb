# T-203 – `store.Open`: escape the database path in the SQLite URI

- **Owner:** pl-reviewer (implementer; was a Codex-size card)   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S (≤ ~40 lines)   · **Depends on:** –

## Goal
Found in T-190: `store.Open` builds `file:<path>?…` without escaping. A data dir with `#` or `?` in its path opens another file (two dirs `seed#0`, `seed#1` shared one DB in a fuzz run). The same pattern may be in `plumb backup`, `plumb restore` and the verify path.

## Acceptance criteria
- [ ] Every place that builds a SQLite URI from a path escapes it (one helper). List the places in the PR.
- [ ] Tests: paths with `#`, `?`, `%`, a space, a non-ASCII letter open the file with exactly that name; two such dirs never share a DB.
- [ ] Say whether a real install could be hit (the installer's data dir is fixed) and whether `--data-dir` of `serve`, `backup`, `restore` are.
- [ ] `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

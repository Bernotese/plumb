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
branch `pl-reviewer/T-203-sqlite-uri` · tests: `make lint test` → pass
- Cause: modernc opens with `SQLITE_OPEN_URI` and splits its own options at the first `?`; `file:<path>?…` let `#` end the name (`seed#0` → `seed`), `?` move the pragmas into the name, `%XX` be decoded.
- One place builds a URI: `internal/store/store.go` `Open` (now `fileURI`: every byte but unreserved and `/` percent-encoded; absolute paths get `file:///`). `serve` (`--data`), `plumb backup` (`--data`), `plumb restore` (`--data`, also its verify/migrate step `restoreDB`) all reach SQLite only through it. `VACUUM INTO ?` binds a plain path (no URI form for an absolute path).
- Real install: not hit; `install.sh` fixes the data dir at `/var/lib/plumb`. Hit only by a person's `--data` with such a character, or `--dev` with a `TMPDIR` that has one.
- Tests: `TestOpenPathEscape` (`#`, `?`, `%`, space, `ü`, `;&=`): the DB SQLite reports is the exact path, pragmas on, no shared DB, no stray file; it fails on the old line (`seed#0` opened `seed`). `TestFileURI` for the encoding.

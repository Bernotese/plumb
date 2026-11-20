# T-054 – Installer: two minors from the real host

- **Owner:** pl-codex-fix (run `codex-usage --route` first; else pl-backend)   · **Reviewer:** pl-reviewer   · **QA:** tests in `./install/`
- **Size:** S   · **Depends on:** T-013 on main   · **State:** ready

## Goal
Two small defects that the repair run on the test host showed (log: `docs/tasks/T-053.log.md`, steps 1 and 1b).

## Context (read only these)
- `install/install.sh`, the tests in `install/`
- `docs/tasks/T-053.log.md` steps 1, 1b

## Acceptance criteria
- [ ] No child process of the installer reads the terminal before the `Continue?` prompt: every `pct exec` / `lxc-attach` call that is not the key init gets `</dev/null`. Test: an answer that is typed before the prompt is still read by the prompt (the fake `pct` reads stdin when it is a terminal or a pipe).
- [ ] `--dry-run` does not print the key-init step when the key exists (same rule as the real run). Test.
- [ ] `plumb key init` still gets the real terminal (D-178). The key tests stay green.
- [ ] Optional, only if it is a few lines: after the key block, the installer waits for Enter with the text "Write the recovery key down, then press Enter" (review minor of PR #26). Not with `--yes`? Decide and say it in the PR.
- [ ] `go test -count=1 ./install/` and `shellcheck` pass.

## Out of scope
- Any change of rights, storages, the unit or the snippet.

## Result (owner, max 8 lines)
branch · commit · tests · notes

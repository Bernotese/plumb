# T-128 – Installer: two small findings of the T-122 host run

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** T-114 (on main)

## Goal
Fix two small installer findings (T-122 F1, F2).

## Context (read only these)
- `install/install.sh`: `storage_field` / `json_rows`; the `install -d -m 0755` for `backup-copy.conf` (around line 1505)

## Acceptance criteria
- [ ] F1: `--backup-storage <name>` with a storage that does not exist prints only the clear refusal; no stray Perl "malformed JSON string" line (empty `pvesh` output handled)
- [ ] F2: the host directory `/etc/plumb` never gets wider than 0700 during a run, also when the run dies in between
- [ ] A fake-PVE or shell test for each; shellcheck clean; `make lint test build` green; small diff

## Out of scope
- Anything else in the installer

## Result (owner, max 8 lines)
branch · commit · tests · notes

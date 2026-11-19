# T-266 – Leak hunt, RC and host part (release slice C5b)

- **Owner:** pl-reviewer   · **Reviewer:** pl-backend (the method was reviewed in #389); `pl-lead` reads the hit list   · **QA:** –
- **Size:** S   · **Depends on:** T-263 at its C5b hold

## Goal
Release slice, card **C5b** of `docs/plans/release-slice.md` (section 5a "P4", P1 point 7). No secret is in the release candidate or in what the controller of the RC wrote on the real host.

## Acceptance criteria
- [ ] Scan `~/.config/plump/t263-c5b/` (0700, made by `pl-backend` at the hold; you do not go to the host) with `scripts/leak-scan.py` (`--dir`): both RC tarballs and their members (binaries as raw bytes), the DB copy, the journal, the events, the export and the Plumb backup, the guest backup listing and the sample volume, the installer raw outputs. Exact values from the dev VM files incl. `t263-secrets.json` and `t263-a-recovery.key` if it exists; "unreadable > 0" is a finding.
- [ ] Say per item what a hit would mean: a secret in the DB copy or the encrypted backup is expected only in its encrypted form (the DB holds sealed values: prove that no plain value is there); a token bearer or password in the journal, the events, the export or a raw output is a finding; in an RC tarball it stops the release.
- [ ] Also the repo part again for what merged since T-256 (refs, PR texts and Actions logs since #389).
- [ ] Output = places and kinds only; the hit list in a 0600 file outside the repo. **On a hit: stop and report to `pl-lead` at once.**
- [ ] When done: tell `pl-lead`; `pl-backend` deletes the folder. The log (method, counts) as `docs/tasks/T-266.log.md` in a PR; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

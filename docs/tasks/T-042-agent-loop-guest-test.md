# T-042 – Agent loop on a real guest

- **Owner:** pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S–M (two PRs if the diff passes ~150 lines)   · **Depends on:** T-018 on `main` (done), guest harness (done)

## Goal
The CI guest job proves the whole agent loop as root in a real systemd guest, not only the resource types.

## Context (read only these)
- `docs/tasks/T-007.run-review.md` – section C and proposal 7 (this card), proposal 4 (how to run a second test binary in the guest)
- `internal/agent` – `TestAgentWithRealLink` (setup to reuse)
- `test/guest/run.sh`, `.github/workflows/ci.yml`

## Acceptance criteria
- [x] `internal/agent/guest_test.go` with build tag `guest`: real link and TLS on loopback, `resource.ExecRunner` on `/`, `state_dir` under `/var/lib/plumb-guest` (root, 0700).
- [x] Steps: enroll → apply a `file` and a `service` with `planned` → result `applied`, and no drift item for these ids within 2× the debounce → edit the file by hand → one `differs` drift item within the debounce max wait.
- [x] A `state_dir` with mode 0755 is refused.
- [x] A canary: the real agent applies a resource with a secret value; real `systemctl` or tool output that holds the value is masked in what the controller stores (review note on PR #21, D-139).
- [x] Leftover `.desired.json-*` and `.credential-*` temp files are removed at agent start in the guest (PR #64).
- [x] The test runs in the CI guest job on Debian 13 and Ubuntu 26.04 and keeps the job under 5 minutes.
- [x] No production changes. Report bugs. Decisions, if any: `docs/decisions/T-042.md`.

## Out of scope
- The slow-apply overlap case (M2 of the PR #13 review). It needs a slow package and makes CI time unstable.

## Result (owner, max 8 lines)
- Core: `pl-codex-qa/T-042-agent-loop-guest` · `5203140` · PR #69.
- Follow-up: `pl-codex-qa/T-042-guest-state-secret` · `5c6b887` · PR #70 (stacked on #69).
- Tests: guest compile/vet pass; CI guest job 130007214115 passes on Debian 13 and Ubuntu 26.04 (1m46s).
- `TestGuestAgentLoop`: 4.18s / 4.38s; real TLS, planned apply, inotify drift, root state/temp rules, real apt stderr canary.
- No product changes or product bugs found. The 0755 state check exercises persistence refusal; New logs the cleanup warning by design.

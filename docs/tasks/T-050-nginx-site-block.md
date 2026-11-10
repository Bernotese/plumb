# T-050 – Starter Block `nginx-site` comes back

- **Owner:** pl-codex-fix (run `codex-usage --route` first; else pl-reviewer)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-051)
- **Size:** S   · **Depends on:** T-047   · **Parent:** T-044

## Goal
`nginx-site` is a starter Block again and applies clean in the first Run on a new Target.

## Context (read only these)
- `docs/decisions/T-044.md` – D-173, D-165
- `git show 859eddf:blocks/nginx-site.yaml` and `git show 859eddf -- blocks/render_test.go blocks/blocks_test.go blocks/constraints_test.go` – the Block and tests of PR #73
- `blocks/nginx.yaml`, `blocks/blocks_test.go`

## Acceptance criteria
- [ ] `blocks/nginx-site.yaml` from 859eddf, plus `after: [package:nginx]` and `restarts: [service:nginx]`. The description has no override text; it says: use it with the nginx Block.
- [ ] `blocks/nginx.yaml` description: add "Add more sites with the nginx-site Block."
- [ ] `blocks/embed.go`: `//go:embed *.yaml`, exported `FS` (T-051 renders the real files in the guest test).
- [ ] Tests in `blocks`: `nginx-site` in `TestBlocks` and the constraint tests; render `nginx` + two `nginx-site` uses in both Target orders: `restart_on` of `service:nginx` has 6 ids, the two site files have `after: [package:nginx]`; a Target with `nginx-site` and no `nginx` fails and names `package:nginx`.
- [ ] `go test ./blocks ./internal/state` and `make lint test` pass.

## Out of scope
- Guest and slice tests (T-051). Changes to render or the agent: report them, do not fix them here.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-086 – test flake: TempDir cleanup race in the issue env tests

- **Owner:** pl-codex-fix   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S (Codex, ~20 lines)   · **Depends on:** –

## Goal
`TestFailedTargetDoesNotStopOthers` no longer fails at `t.TempDir` cleanup under load. Test-only; the product is safe (diagnosis of pl-architect, 2026-11-12).

## Context (read only these)
- `internal/issue/env_test.go` – the test env: `a.Run` goroutines and the hello loop still hold a SQLite connection when cleanup calls `st.Close()`.

## Acceptance criteria
- [ ] The env has a WaitGroup for `a.Run` and the hello loop; the hello loop uses the env ctx, not `context.Background()`.
- [ ] Cleanup order: cancel → `wg.Wait` → `eng.Close` → `st.Close`.
- [ ] In the test: cancel the Subscribe ctx and drain `sub.C` until it closes.
- [ ] No product code changes.
- [ ] Tests: `go test ./internal/issue -run TestFailedTargetDoesNotStopOthers -count=200` under CPU load → 0 failures; `make lint test build` passes.

## Out of scope
- Any change outside `internal/issue/*_test.go`.

## Result (owner, max 8 lines)
branch · commit · tests · notes

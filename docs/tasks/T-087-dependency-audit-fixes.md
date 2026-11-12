# T-087 – audit findings: Go patch level and web dev dependencies

- **Owner:** pl-codex-fix   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** –

## Goal
`make vuln` and `npm audit` in `web/` are clean (QA audit at the end of T-038, 2026-11-12).

## Context (read only these)
- `go.mod` (go / toolchain line), `.github/workflows/*` (Go version), `Makefile` (`vuln`)
- `web/package.json`, `web/package-lock.json`

## Acceptance criteria
- [ ] Go: the toolchain is the patch level that fixes GO-2026-6859 in `net/http` (1.27.4), in `go.mod` and CI alike; `make vuln` → no finding in called code.
- [ ] Web: the `deepmerge` and `minimatch` chains (12 findings, low/moderate) are fixed with the smallest version changes (`npm audit fix` without `--force`; an `overrides` entry only if needed). Say in the report which findings stay and why (dev-only or runtime).
- [ ] No source change besides version files. TypeScript stays at the pinned 5.9.
- [ ] Tests: `make lint test build` passes.

## Out of scope
- Major version upgrades.

## Result (owner, max 8 lines)
branch · commit · tests · notes

# T-087 – audit findings: Go patch level and web dev dependencies

- **Owner:** pl-reviewer (not trivial for pl-codex-fix)   · **Reviewer:** pl-architect   · **QA:** –
- **Size:** S (Codex)   · **Depends on:** –

## Goal
`make vuln` and `npm audit` in `web/` are clean (QA audit at the end of T-038, 2026-11-12).

## Context (read only these)
- `go.mod` (go / toolchain line), `.github/workflows/*` (Go version), `Makefile` (`vuln`)
- `web/package.json`, `web/package-lock.json`

## Acceptance criteria
- [x] Go: the toolchain is the patch level that fixes GO-2026-6859 in `net/http` (1.27.4), in `go.mod` and CI alike; `make vuln` → no finding in called code.
- [x] Web: the `deepmerge` and `minimatch` chains (12 findings, low/moderate) are fixed with the smallest version changes (`npm audit fix` without `--force`; an `overrides` entry only if needed). Say in the report which findings stay and why (dev-only or runtime).
- [x] No source change besides version files. TypeScript stays at the pinned 5.9.
- [x] Tests: `make lint test build` passes.

## Out of scope
- Major version upgrades.

## Result (owner, max 8 lines)
- Branch `pl-reviewer/T-087-dependency-audit-fixes`. Tests: `make lint test build` → pass; `make vuln` → 0 in called code.
- Go: `toolchain go1.27.4` in `go.mod`; CI reads it via `go-version-file: go.mod`. Dev VM Go is now 1.27.4 (old: `~/.local/opt/go1.27.3`).
- Go module-only finding stays: GO-2026-5932 (`x/crypto/openpgp`, not imported, not called; fix only in a pseudo-version).
- minimatch (10 findings, moderate): override `minimatch@>=7.0.0 <11.0.1` → 11.0.8 (eslint, @eslint/config-array, typescript-estree). No 10.x fix exists. eslint uses `Minimatch#match(path, partial)`, `pattern`, `dot`, `flipNegate`; all still in 11. Check: `eslint -f json .` before/after → same 92 files, same results. redocly's minimatch 5.1.9 is not affected and unchanged.
- deepmerge (2 findings, low): stays. No fixed version. Only `@sveltejs/vite-plugin-svelte` uses it, at build time, to merge our own plugin options. Dev-only, not shipped (`web/dist` has no deepmerge). Re-check when vite-plugin-svelte drops or replaces it.

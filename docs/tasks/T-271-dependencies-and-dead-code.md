# T-271 – Dependencies and dead code (cleanup point 4)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** the CI jobs
- **Size:** M   · **Depends on:** –

## Goal
User order D-442, point 4. The first release ships no unused module, package, file, script or Makefile target.

## Acceptance criteria
- [ ] Go: `go mod tidy` gives no diff; no module that nothing imports; `govulncheck ./...` clean; the pinned toolchain stays (T-255).
- [ ] Web: unused npm packages removed (dependencies and devDependencies; prove each with a search or a tool such as `depcheck`/`knip`, and name false positives), `npm audit` clean, the lockfile updated; `web/dist` still has no `.map` file.
- [ ] Dead code: unused Go functions, types, files (a tool such as `deadcode`/`staticcheck -unused`, then read each hit: test helpers and the generated API types are not dead), unused web files and exports, unused scripts in `scripts/` and `test/`, unused Makefile targets (a target that CI or a doc names is used). Each removal in the PR's table with the proof that nothing uses it.
- [ ] Do not remove a safety check, a tripwire test, a planned-field guard or anything a decision names, even if a tool calls it unused: list such hits and keep them.
- [ ] Small PRs by topic (Go modules, web packages, dead Go code, dead web code, scripts and Makefile); each with `make lint test`, green CI in all four jobs, the evidence comment first. After the last one: the build is still reproducible (the dev VM build equals the CI build), and say that the release hashes change (RC5).
- [ ] No behaviour change. If a removal changes behaviour, stop and ask `pl-lead`.

## Result (owner, max 8 lines)
branch · commit · tests · notes

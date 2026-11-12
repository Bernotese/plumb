# T-076 – template repack `ctpack`, zstd dependency, first-boot units

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** – (no host)
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
A pure-Go package repacks a Proxmox system template with the agent, its config and the first-boot units, inside hard limits.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §2, §6, §9 (D-225, D-230, D-236)
- `docs/tasks/T-038.log.md` (host facts A, D)

## Acceptance criteria
- [ ] Everything in §2 of the plan: byte-exact base copy, appended USTAR entries, limits of D-236, decoder options of D-225, closed on every path incl. cancel.
- [ ] Tests of §9 for `ctpack` (incl. hostile archives and the size caps); fuzz the tar/zstd input.
- [ ] `govulncheck` clean; one new module only.
- [ ] Tests: `make lint test build` passes.
- [ ] New decisions go to `docs/decisions/T-076.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- The fetch (D-237) if the plan puts it in the driver card; the driver; Proxmox calls.

## Result (owner, max 8 lines)
`pl-frontend/T-076-ctpack` · `make lint test build` → ok; `go test -fuzz FuzzPack` 45 s clean; real Debian 13.6-1 (`PLUMB_CT_FIXTURE`): 208 MB `.tar.gz` in 4.2 s, base byte-equal.
- API: `ctpack.Pack(ctx, src io.ReadSeeker, dst io.Writer, Options{SHA512, CA, AgentURL})` (pin check first), `ctpack.Name(image, o)`, `ctpack.TokenEnv`. The fetch, temp files and upload stay in T-079.
- install.sh snippet: binary goes to `/usr/local/bin/.plumb-agent.new`, then `mv` (S4); `install_test` keeps snippet script/unit equal to `ctpack`.
- `make vuln` (govulncheck v1.9.0) + CI step. Findings today are not from the new module: net/http GO-2026-6859 (fixed in go1.27.4; local Go is 1.27.3), x/crypto openpgp (not called).
- For T-078/T-082: `wire.IsAgentPath` does not cover `plumb-agent-install.service` and its wants link; seal must remove them in a container.

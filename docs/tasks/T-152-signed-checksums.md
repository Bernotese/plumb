# T-152 – Signed image checksums and pin refresh

- **Owner:** pl-architect (implementer)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa (T-157)
- **Size:** L   · **Depends on:** T-144 merged; `unverified-pin` in `humanonly.go` only after T-148 merged; route line after T-147

## Goal
Slice 5, child card **C8** of T-138. Plumb checks vendor signatures of base images and can refresh a pin by a Plan; scheduled builds take a new signed pin by `system`.

## Context (read only these)
- Plan: `docs/tasks/T-138-road-to-release-slice-5-plan.md` section "Plan", decisions D-327 … D-347 (`docs/DECISIONS.md`; until merged in `docs/decisions/T-138.md`, `T-144.md`), contract: `api/openapi.yaml`, `docs/ARCHITECTURE.md` §17. Read row C8 of the child card table, the contracts, the risks, and the review comments of PR #241 / #243 that name this row.
- `AGENTS.md` "Autonomy and release" (D-326)

## Acceptance criteria
- [ ] Everything row C8 of the plan lists; touch only the files that row gives you. If you need another file, ask `pl-lead` first.
- [ ] Package `internal/trust` with fuzz tests for every parser. One pinned key per source. Signature time strictly newer than the last accepted one; no stored time = a person approves. Proxmox: verify `aplinfo-pve-9.dat.asc` over the unpacked `.dat`. Debian VM images (no signature): refresh stays human.
- [ ] New decisions (only if needed): ask `pl-lead` for numbers; write them to `docs/decisions/T-152.md`.
- [ ] No new token right. Tests: `make lint test` passes; `origin/main` merged; PR to `main`; report to `pl-lead`.

## Out of scope
- Everything of other rows. Any release step.

## Result (owner, max 8 lines)
branch · commit · tests · notes

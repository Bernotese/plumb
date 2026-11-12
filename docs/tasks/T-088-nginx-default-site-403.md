# T-088 – the `nginx` Block's default site answers 403 on Debian and Ubuntu

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** S   · **Depends on:** –
- User decision 2026-11-12: fix it, for VMs and LXC.

## Goal
After a Run with only the Block `nginx`, `GET /` on the guest answers 200, on Debian 13 and Ubuntu 26.04, in a VM and in a container.

## Context (read only these)
- The `nginx` Block and its default site (find it with a search for the Block name); `docs/tasks/T-083.log.md` H1 (the fact: Debian ships `index.nginx-debian.html`, the site config asks for `index.html` → 403).
- The guest tests (`make test-guest`, run with `sg docker -c "make test-guest"`).

## Acceptance criteria
- [ ] Find the cause first and write it in the Result. Pick the smallest fix; one decision line (D-255) in `docs/decisions/T-088.md` says what the Block owns.
- [ ] Plumb never overwrites or deletes a page that a person or another Block (`nginx-site`) put there; no drift Issue appears for a guest that was fine before.
- [ ] An existing guest gets the fix with the next Run and shows it in the Plan (no silent change).
- [ ] Guest test: after the Block, `curl` on localhost → 200, Debian 13 and Ubuntu 26.04.
- [ ] Tests: `make lint test build` and the guest tests pass.

## Out of scope
- Any other Block. Real-host run (pl-lead adds it to the next host job).

## Result (owner, max 8 lines)
branch · commit · tests · notes

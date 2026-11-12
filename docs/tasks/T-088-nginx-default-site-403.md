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
- Branch `pl-frontend/T-088-nginx-default-site`. Cause: the Block's site config had `index index.html`; the Debian/Ubuntu package ships only `/var/www/html/index.nginx-debian.html` → 403.
- Fix: one line in `blocks/nginx.yaml`: `index index.html index.htm index.nginx-debian.html;` (the distro list). The Block writes no page; a person's or `nginx-site`'s `index.html` still wins. D-255 in `docs/decisions/T-088.md`.
- Existing guest: the stored Block changes only by a Plan (file content, reversible + `service:nginx` restart, disruptive). Agent drift compares with its last applied list → no Issue before that Plan. Starter Blocks are not seeded into a controller; the user puts the new Block.
- Tests: `make lint test build` → rc 0; `sg docker -c "make test-guest"` → rc 0 (Debian 13 + Ubuntu 26.04: default site `curl` 127.0.0.1 → 200). Mutation (old index line) → guest test fails with 403. New unit test `TestNginxDefaultSiteServesDistroPage` (index line, no resource under `/var/www`).

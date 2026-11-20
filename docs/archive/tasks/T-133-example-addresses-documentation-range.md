# T-133 – Example addresses in the repo use the documentation ranges

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (mechanical, about 70 places)   · **Depends on:** T-131 (PR #232, touches `cmd/plumb/dev.go`)

## Goal
No example address in tests, docs, the API description or demo data is from a private home range. All use the documentation ranges 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 (RFC 5737) (user order, 2026-11-13).

## Context (read only these)
- `git grep -nE '\b(192\.168|10\.|172\.(1[6-9]|2[0-9]|3[01]))\.[0-9]+\.[0-9]+'` over the repo (not `node_modules`, not `web/dist`)

## Acceptance criteria
- [ ] Every example IPv4 address from 192.168.0.0/16, 10.0.0.0/8 and 172.16.0.0/12 in tests, docs, `api/openapi.yaml`, schemas, Blocks, scripts and demo data is replaced by one from the three documentation ranges; address + prefix + gateway stay consistent with each other
- [ ] Keep the meaning of a test. Where a test needs a private, loopback, link-local or bridge address **as such** (e.g. a classifier of private ranges, the Docker bridge `172.17.0.1`, the dev-only `10.99.0.x` simulation, the `browser-shot` host URL in AGENTS.md), keep it and list it in the PR with the reason. Prefer `10.` or `172.` examples that are clearly generic there, never `192.168.1.x`
- [ ] IPv6 examples: use `2001:db8::/32` where an example is not already from it
- [ ] The git history is not rewritten (no force-push)
- [ ] Do not read or print `~/.config/plump/testhost.env`; pl-lead runs the leak check after the merge
- [ ] `make lint test build` green; e2e green; the PR lists what stayed and why

## Out of scope
- Behaviour changes; the README pictures (already on the documentation range)

## Result (owner, max 8 lines)
branch · commit · tests · notes

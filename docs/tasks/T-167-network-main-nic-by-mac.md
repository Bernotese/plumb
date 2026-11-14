# T-167 – network: find the main NIC entry by MAC match (host finding F3)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
On a stock Ubuntu 26.04 VM the first boot keeps the NIC name `ens18` (D-192), while cloud-init's netplan entry is `eth0` with `match.macaddress` + `set-name`. `mainKey` matches by key, `set-name` or `match.name` only, so `network` fails (closed) with "netplan has 0 entries for the main NIC" until a reboot. It must work without the reboot.

## Context (read only these)
- `docs/tasks/T-158.log.md` (PR #265; finding F3)
- `internal/resource/network_apply.go` (`mainKey`) · D-348, D-354 · `docs/plans/debian-network-facts.md` on branch `pl-backend/T-160-debian-network-facts`

## Acceptance criteria
- [ ] `mainKey` also matches an entry whose `match.macaddress` equals the MAC of the main NIC (compare case-insensitive; the MAC is read from the live link, never copied into the Plumb file – D-348 'no MAC copy' stays).
- [ ] Still exactly one entry or a clear refusal; two entries that match (one by name, one by MAC) refuse.
- [ ] Unit tests for: key, set-name, match.name, match.macaddress with a different live name, none, two. CI guest test: a VM whose NIC name differs from the netplan key (rename the link or write such a netplan file) applies `network` without a reboot.
- [ ] `make lint test` passes; own worktree and branch; PR to `main`; report to `pl-lead`.

## Out of scope
- Anything else of the log.

## Result (owner, max 8 lines)
branch · commit · tests · notes

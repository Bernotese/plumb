# T-200 – `TestGuestFirewall`: say why a port is not reachable when it fails

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S (test-only)   · **Depends on:** –

## Goal
On PR #281 the VM job 132339625102 failed once: `guest_firewall_test.go:236` "port 8080 reachable = false, want true" while the dumped table accepts `tcp dport 8080`. Not reproduced (20/20 local, 1 fail in the last 100 CI runs). Cause unknown; it is **not** proven to be test-only. The next failure must give the cause.

## Acceptance criteria
- [ ] On a failed reach check the test dumps: `nft list ruleset`, `ip -br addr`, `ip route`, `ss -ltn`, `conntrack -L` (if present), the result for the neighbour port, and the time since the listener started and since the apply returned.
- [ ] No retry that could hide a real fault: the assertion and its timeout stay as they are.
- [ ] Open point kept in `docs/STATUS.md` by `pl-lead` until a failure with the dump is explained.
- [ ] `make lint test`; PR to `main`; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes

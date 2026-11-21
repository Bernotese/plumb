# T-207 – Repair: host key accept works for every key type (host finding F9)

- **Owner:** pl-backend (author of T-191)   · **Reviewer:** pl-reviewer (repair path)   · **QA:** –
- **Size:** S–M   · **Depends on:** –

## Goal
Found on the real host (T-192, F9; fails closed): accepting a changed host key works only for the ECDSA fingerprint, because the Go SSH client prefers ECDSA and Plumb sets no `HostKeyAlgorithms`. Accepting the ed25519 or RSA key then fails "not pinned". Also: a fingerprint that is not in `host_keys_seen` still gets a Plan, and `repair.state` stays `ready` while the keys differ.

## Acceptance criteria
- [ ] The client offers only the algorithms of the pinned keys for this guest (`HostKeyAlgorithms` from the pins), so the key the person accepted is the key that is checked; with pins of several types any pinned one passes, an unpinned type never.
- [ ] Accept refuses (422/409, no Plan) a fingerprint that the agent never reported for this Target (`host_keys_seen`); the Plan text shows type + fingerprint old → new.
- [ ] `repair.state` is not `ready` while the reported keys differ from the pins; the reason says so.
- [ ] Tests for ed25519, ECDSA, RSA (accept each, then repair), the fake fingerprint, the state. CI guest test in the VM job with a guest that has only an ed25519 host key and one that has all three.
- [ ] `make lint test`; PR to `main`; report. Proven again on the host in T-192.

## Result (owner, max 8 lines)
branch · commit · tests · notes
